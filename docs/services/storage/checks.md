# Ledger Drift Checks

The **Ledger Drift Checks** compare what a node has stored against what the ledger
says, and report every place the two disagree. They run in the background on a
live node, on an interval, and record what they find so that a problem which
persists is aged rather than reported afresh every time.

Three of the checks predate this service and were only ever reachable through the
on-demand `Check` call on the auditor and owner services, which nothing invokes
on a running node. Drift, however, appears while a node is up: a crash between
writing a token and committing its transaction, a rollback the node missed, a
public parameters update that leaves an old token unspendable. This service is
what makes the checks run at the time they can still catch that.

## Checks

| Check | What it compares | Direction |
|---|---|---|
| Transaction Check | The status of every locally stored transaction against the ledger's | local to ledger |
| Unspent Tokens Check | The content of every locally held unspent token against the ledger's copy | local to ledger |
| Token Spendability Check | Every locally held unspent token against the current TMS: supported format, parsable, recipients still verifiable | local only |
| Local Completeness Check | The outputs every confirmed transaction should have written locally against what is actually stored | ledger to local |

The first three start from what the node already holds, so a token that was never
written locally is invisible to them. The Local Completeness Check exists for that
case: it rebuilds each confirmed transaction's token request, asks the token
service which tokens that transaction should have appended, and reports the ones
the store does not have. When one is missing, the ledger decides how serious it
is: still unspent means the node owns money it cannot see, already spent means the
books are wrong but nothing is lost.

Because it rebuilds every confirmed transaction, that check is more expensive than
the others. It runs in the background sweep only; the on-demand `Check` call keeps
running the three cheap ones.

## Findings

A check reports a `Finding` rather than a message:

```go
type Finding struct {
    Checker  string      // the check that produced it
    Code     string      // what kind of divergence, e.g. "token_missing_locally"
    Severity Severity    // info, warning or critical
    TxID     string      // the transaction it is about, if any
    TokenID  *token.ID   // the token it is about, if any
    Message  string      // human-readable description
}
```

`Finding.Key()` is the stable identity of the finding: the same underlying problem
produces the same key on every sweep. It deliberately excludes the message and the
severity, both of which can carry a detail that moves (a ledger status code, an
error string) without the problem itself changing.

### Severity

| Severity | Meaning |
|---|---|
| `info` | Expected to resolve on its own, for example a ledger that has not caught up yet |
| `warning` | Will not resolve on its own but costs the node nothing it cannot recover |
| `critical` | Loses the node money or makes it unable to spend what it owns |

Critical findings are logged individually at error level on every sweep; a count
alone is not actionable when the subject is money.

### Codes

| Code | Reported when |
|---|---|
| `tx_status_mismatch` | The local status of a transaction disagrees with the ledger's |
| `tx_status_unavailable` | The ledger status of a transaction could not be retrieved |
| `tx_request_missing` | A transaction record has no token request stored beside it |
| `tx_request_unparsable` | A stored token request cannot be parsed with the current TMS |
| `token_content_mismatch` | A locally held token does not match the ledger's copy |
| `token_missing_on_ledger` | A token held as unspent is not on the ledger |
| `token_missing_locally` | The ledger holds a token for this node that never landed locally |
| `token_format_unsupported` | An unspent token's format is not one the current TMS supports |
| `token_not_deobfuscatable` | An unspent token cannot be deobfuscated with the current TMS |
| `token_no_recipients` | A token deobfuscates to an empty recipient list |
| `token_recipient_unverifiable` | No owner verifier can be obtained for one of a token's recipients |
| `check_failed` | A check could not complete, so its part of the sweep proved nothing |
| `unclassified` | A message from a custom check that still returns plain strings |

`check_failed` is deliberately distinct from a clean sweep. A check that could not
reach the ledger has established nothing, and treating that as "found nothing"
is the failure this service exists to avoid.

## Storage

Findings are stored in the transaction store, in a table keyed by the finding key:

| Column | Meaning |
|---|---|
| `finding_key` | The stable identity, and the primary key |
| `checker`, `code`, `severity`, `tx_id`, `token_id`, `message` | The finding itself, as of its last sighting |
| `first_seen` | When the finding was first recorded |
| `last_seen` | When it was last observed |
| `occurrences` | How many sweeps have observed it |
| `resolved_at` | When it stopped being observed, `NULL` while open |

A sweep upserts everything it found with the same timestamp, then closes the open
findings that timestamp did not touch. A finding observed again after being closed
is reopened rather than duplicated.

Two rules keep that from closing something real:

- A check that reported `check_failed`, or any other code meaning a part of it
  was inconclusive (for example `tx_status_unavailable`, reported when a single
  transaction's ledger status could not be retrieved), never has any of its
  findings closed by that sweep. Otherwise a ledger outage on one transaction
  could close a critical finding recorded for that same transaction earlier.
- A check that does not look at everything it could report on never has its
  findings closed at all. That is the case for the history-walking checks when
  `transactionWindow` is set, and for custom checks registered through dependency
  injection, since nothing is known about their coverage.

A sweep with more findings than fit in one upsert statement (roughly 3000 on
SQLite, comfortably more on Postgres) persists them in chunks that all share
the sweep's one timestamp, rather than failing the whole sweep over a bind
parameter ceiling.

## Leader election

Only one replica sweeps a given store at a time, decided by a PostgreSQL advisory
lock, so several replicas on one database multiply neither the ledger traffic nor
the findings. The lock id is not configurable: it is derived automatically from
the store's own network, channel, namespace and role, which is what makes each
store's sweep unique in the first place, so TMSes sharing a persistence
configuration never collide on the same lock. It is also distinct from the one the
[Transaction Recovery Service](recovery.md) uses, so the two sweeps do not wait on
each other.

On SQLite the advisory lock has no effect, which is fine for the single-node
deployments SQLite is meant for.

A node runs one sweep per store it owns: `owner` over the `ttxdb` and `auditor`
over the `auditdb`. Every log line and every metric carries that role.

## Configuration

```yaml
services:
  storage:
    checks:
      enabled: true            # Run the background sweep
      scanInterval: 1h         # How often to sweep
      timeout: 30m             # Bound on one sweep
      batchSize: 50            # Tokens resolved against the ledger per round trip
      transactionWindow: 0s    # Only look at transactions this recent; 0 means all
```

`transactionWindow` bounds the cost of a sweep on a node with a long history, at
the price of the history-walking checks no longer closing their own findings. Leave
it at zero unless a sweep is approaching its timeout.

## Metrics

| Metric | Type | Labels |
|---|---|---|
| `storage_checks_sweep_duration_seconds` | histogram | `role` |
| `storage_checks_sweeps_total` | counter | `role`, `outcome` (`completed`, `not_leader`, `failed`) |
| `storage_checks_findings_observed_total` | counter | `role`, `checker`, `code`, `severity` |
| `storage_checks_findings_open` | gauge | `role`, `severity` |
| `storage_checks_findings_resolved_total` | counter | `role` |

All of them also carry `network`, `channel` and `namespace`.

> **Note:** the names above are the bare `Name:` fields declared in the source, not what Prometheus
> exports. These metrics are built through a `NewTMSProvider`-wrapped provider, so each is actually
> exported as `panurus_core_common_metrics_<name above>` — see
> [Metrics Reference](../../development/metrics.md#ledger-drift-checks) for the exact exported names.

`storage_checks_findings_open` is the one to alert on: it is a level, so it drops
back to zero once a problem stops being reported, unlike the observed counter which
records sightings. A node reporting only `not_leader` sweeps is not the replica
doing the checking, which is the intended behaviour with several replicas on one
database.

## On-demand checks

The `Check` call on the auditor and owner services is unchanged and still returns
plain messages, now prefixed with the severity and code of the finding behind them:

```
[critical][token_format_unsupported] token format not supported [tx1:0][fabtoken.v1]
```

## Writing a custom check

A custom check registered through dependency injection into the `ttxdb-checkers`
or `auditdb-checkers` group still returns plain messages, and is lifted into a
finding automatically:

```go
func myCheck(ctx context.Context) ([]string, error) {
    // ...
}

common.NamedChecker{Name: "My Check", Checker: myCheck}
```

Such a finding has no code and no ids to key on, so its key falls back to the
message. A message carrying a timestamp or a counter will therefore look like a
new finding on every sweep. A check that wants to be aged properly should return
findings instead:

```go
common.NamedFindingChecker{
    Name:     "My Check",
    Complete: true, // this check looks at everything it could report on
    Checker: func(ctx context.Context) ([]common.Finding, error) {
        return []common.Finding{{
            Checker:  "My Check",
            Code:     "my_code",
            Severity: common.SeverityWarning,
            TxID:     txID,
            Message:  "something is off",
        }}, nil
    },
}
```

Set `Complete` only when one run of the check really does see everything it could
ever report on. It is what allows the sweep to close the check's findings when
they stop being reported, and getting it wrong silently discards real problems.

## Related

- [Transaction Recovery Service](recovery.md) - recovers transactions stuck pending
- [Keystore Cleanup Service](keystore_cleanup.md) - removes keys of deleted tokens
- [Storage Service](../storage.md) - the stores these checks read

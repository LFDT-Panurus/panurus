# Token Chaincode Query Limits

This page describes the limits enforced on the token chaincode's read-only query functions, why
they exist, and how to configure them.

## Why limits exist

The token chaincode (`token/services/network/fabric/tcc`) exposes three read-only query functions:

| Function | Argument | Work per element |
| --- | --- | --- |
| `queryStates` | JSON array of state keys | one `GetState` |
| `queryTokens` | JSON array of `token.ID`s | one `GetState` |
| `areTokensSpent` | JSON array of token/serial-number keys | one `GetState` |

Every element the caller supplies translates 1:1 into a ledger read inside a *single* chaincode
invocation. The `invoke` path is bounded by `driver.ResourceLimits` (see
[Validator Resource Limits](../drivers/validation-resource-limits.md)), which rejects an oversized
token request before any per-element work. The query path had no analogous guard: a single request
carrying an arbitrarily long array drove an unbounded number of ledger reads, and — unlike `invoke`
— it does not go through the size-limited transaction-submission flow. That is a resource-exhaustion
/ peer-slowdown vector reachable by any client that can call the chaincode's query path, with no
elevated privileges (issue #2050).

## Configuration mechanism

Limits are held in `tcc.QueryLimits` (`token/services/network/fabric/tcc/querylimits.go`) and read
from the `QueryLimits` field of `tcc.TokenChaincode`:

| Field | Default | Checked | Enforced |
| --- | --- | --- | --- |
| `MaxQueryRequestBytes` | 1 MiB | Raw size of the query argument | Before `json.Unmarshal`, so an oversized payload never reaches an allocation proportional to its own size |
| `MaxQueryItems` | 4096 | Number of elements in the decoded array | After the decode and **before the first ledger read** |

`MaxQueryItems` is the meaningful bound — it caps how many ledger reads one invocation can perform.
`MaxQueryRequestBytes` is deliberately high enough that a full 4096-element batch of realistic state
keys still fits, so the two limits do not shadow each other and the binding limit is predictable; it
exists to reject a payload before decoding, including one made of few but enormous elements. Both
defaults are far above the batch sizes produced by any in-tree caller
(`lookup.DeliveryScanQueryByID`, `tokenFetcher.QueryTokens`, `spentTokenFetcher.QuerySpentTokens`).

Violations return a typed error — `tcc.ErrQueryRequestTooLarge` or `tcc.ErrTooManyQueryItems` —
wrapping the effective limit, surfaced to the caller as a chaincode error response.

`QueryLimits.WithDefaults()` overlays `DefaultQueryLimits()` onto any field that is not a positive
value, and the chaincode applies it on every query. Consequently a `TokenChaincode` built without
setting `QueryLimits` is still bounded by the defaults, and a partially-specified override (or a
negative value from a configuration typo) can never silently disable a limit.

The standalone chaincode process (`token/services/network/fabric/tcc/main/main.go`) has no
configuration service wired, so it resolves the limits from the environment via
`tcc.EnvQueryLimitsProvider` — mirroring `tcc.EnvResourceLimitsProvider` for validation limits:

| Environment variable | Field |
| --- | --- |
| `TOKEN_QUERY_MAX_REQUEST_BYTES` | `MaxQueryRequestBytes` |
| `TOKEN_QUERY_MAX_ITEMS` | `MaxQueryItems` |

Each variable is optional; an unset variable leaves the field at zero, which `WithDefaults` then
replaces with the default. An unparseable value is a startup error, not a silent fallback.

## Relationship to the consensus-safety contract

Unlike `driver.ResourceLimits`, these limits are **not** consensus-relevant. The query functions
perform no writes and are reached through the query/evaluate path rather than through endorsement,
so a peer configured with a stricter value only refuses a request another peer would serve — it
cannot make two peers disagree on a transaction's validity. Query limits are therefore an
operational knob per chaincode process, not a value that must be rolled out in lockstep.

## Choosing and changing these values

Clients that legitimately need to look up more than `MaxQueryItems` keys should chunk their
requests. If a deployment instead raises `TOKEN_QUERY_MAX_ITEMS`, keep in mind that the value
directly bounds the ledger reads a single unauthenticated request can trigger — raise it only as far
as observed legitimate batch sizes require, and raise `TOKEN_QUERY_MAX_REQUEST_BYTES` alongside it if
the larger batch no longer fits.

## Testing

- **Exact-boundary unit tests** (`querylimits_test.go`): every field asserts `limit-1` / `limit`
  succeed and `limit+1` fails with the specific typed error, against both the defaults and an
  injected override.
- **Regression tests** (`queryguard_test.go`): for each of the three query functions, an
  over-counted request and an oversized payload are both rejected with **zero** `GetState` calls; a
  request at exactly `MaxQueryItems` is served and performs exactly one read per element; and an
  unconfigured `TokenChaincode` is shown to still be bounded by the defaults.
- **Provider tests**: unset environment (resolves to defaults), partial override (unset fields still
  default), and an unparseable value (returns an error).
- **Fuzzing**: one target per query function, each asserting that for arbitrary caller bytes the
  function never panics and never performs more than `MaxQueryItems` ledger reads. The three are
  separate targets because the surface behind the shared limit check differs:

  | Target | Covers |
  | --- | --- |
  | `FuzzQueryStatesLedgerReadsAreBounded` | array-of-strings decode; each string is used as a ledger key verbatim |
  | `FuzzAreTokensSpentLedgerReadsAreBounded` | same decode, plus validator initialization from the public parameters and — with graph hiding on, fuzzed as a second argument — the key translator's composite-key builder (UTF-8 validation and rune scanning of attacker-controlled text) |
  | `FuzzQueryTokensLedgerReadsAreBounded` | array-of-`token.ID` decode (string + `uint64`) and output-key derivation from each element |

  In-code `f.Add` seeds cover both limit boundaries plus empty, truncated, wrong-shape and
  rejected-rune payloads. `testdata/fuzz/<TargetName>/` holds only what benefits from living on disk
  — the `MaxQueryItems` boundary pair (the limit that bounds ledger reads) and the crash
  reproducers, which Go writes there itself and whose named files report as named subtests. The
  corpus fuzzing *generates* is not committed: it lives in `$GOCACHE/fuzz` and CI restores it only
  best-effort, so these files are the durable floor. All three targets run nightly via
  [`.github/workflows/nightly-fuzz.yml`](../../.github/workflows/nightly-fuzz.yml).

  `FuzzQueryTokensLedgerReadsAreBounded` found a pre-existing nil-pointer dereference on its first
  run: a `null` element in the JSON array decodes to a nil `*token.ID`, which
  `translator.QueryTokens` dereferenced. It now reports a nil entry as an invalid request instead;
  the two payloads that triggered it are kept on disk (`nil-elements-panic-regression`,
  `single-nil-element-panic-regression`), so the panic is caught by a plain `go test` run.

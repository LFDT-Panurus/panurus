# Storage

For an introduction into the concepts of Database, Persistence, Driver, Store, read [this documentation](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/db-driver.md).

SQL is not written by hand in these layers: the stores build it with the query-builder
DSL documented in [SQL Query DSL](./sql-query-dsl.md), which also covers the pagination
strategies and their trade-offs. Where the two SQL backends behave differently -
the decorated write handle, time-offset rendering, and the delivery guarantees of
Postgres notifications - see [Backend-specific behaviour of the SQL
stores](#backend-specific-behaviour-of-the-sql-stores).

The project utilizes the following layers of abstraction on top of the database layer:
* `Store`: executes the SQL queries. A `Store` is only used from within the `StoreService` of the same kind.
* `StoreService`: extends the `Store` (of the same kind) by adding extra functionality (e.g. keeping maps, cache, or combining functionalities of the underlying store). A `StoreService` does not have any other dependency, but the `Store`.
* `Service`: combines `StoreService` and `Service` instances to provide more complete functionality that can be used by the application.
  Each domain has a `Store`, a `StoreService` and potentially a `Service`.

Panurus utilizes a robust data management system to ensure the secure and reliable tracking of all token-related activities.
This system leverages several stores, each with a specific purpose:

* **Transaction Store (`ttxdb`)**:
  This critical store serves as the central repository for all transaction records.
  It captures every token issuance, transfer, or redemption, providing a complete historical record of token activity within the network.
  The `ttxdb.StoreService` store is located under [`token/services/ttxdb`](./../../token/services/storage/ttxdb). It is accessible via the `ttx.Service`.

* **Token Store (`tokendb`)**:
  The `tokendb` acts as the registry for all tokens within the system.
  It stores detailed information about each token, including its unique identifier, denomination type (think currency or unique identifier), current ownership, and total quantity in circulation.
  By referencing the `tokendb`, developers and network participants can obtain a clear picture of the token landscape.
  The `tokendb.StoreService` is used by the `Token Selector`, to select the tokens to use in each transaction, and by the `Token Vault Service` to provide its services.
  The `tokendb.StoreService` service is located under [`token/services/tokendb`](./../../token/services/storage/tokendb). It is accessible via the `tokens.Service`.

* **Audit Store (`auditdb`)** (if applicable):
  For applications requiring enhanced auditability, the `auditdb` provides an additional layer of transparency.
  It meticulously stores audit records for transactions that have undergone the auditing process.
  This functionality is particularly valuable for scenarios where regulatory compliance or tamper-proof records are essential.
  The `auditdb.StoreService` is located under [`token/services/auditdb`](../../token/services/storage/auditdb). It is accessible via the `auditor.Service`.

* **Identity Store (`identitydb`) and Wallet Store (`walletdb`)**:
  The `identitydb` plays a crucial role in managing user identities and wallets within the network.
  It securely stores wallet configurations, identity-related audit information, and so on, enabling secure interactions with the token system.
  The `identitydb.StoreService` is located under [`token/services/identitydb`](./../../token/services/storage/identitydb).
  It also supports **Dynamic Identity Discovery** via the `IdentityNotifier`. This notifier allows services like `LocalMembership` to pro-actively subscribe to new identity configurations added to the database at runtime, ensuring Panurus can pick up new identities without a restart.

## Configuration

Panurus offers flexibility in deploying these databases. Developers can choose to:

* **Instantiate in Isolation:** Each database can operate independently, utilizing a distinct backend system for optimal performance and manageability.
  Here is an example of configuration
```yaml
token:
  tms:
    mytms: # unique name of this token management system
      network: default # the name of the network this TMS refers to (Fabric, etc)
      channel: testchannel # the name of the network's channel this TMS refers to, if applicable
      namespace: tns # the name of the channel's namespace this TMS refers to, if applicable
      # db specific driver
      tokendb:
        persistence: my_token_persistence
```

* **Shared Backend:** Alternatively, a single backend system can be shared by all databases, offering a more streamlined approach for deployments with simpler requirements.
```yaml
token:
  tms:
    mytms: # unique name of this token management system
      network: default # the name of the network this TMS refers to (Fabric, etc)
      channel: testchannel # the name of the network's channel this TMS refers to, if applicable
      namespace: tns # the name of the channel's namespace this TMS refers to, if applicable
```

The specific driver used by the application will ultimately determine the available deployment options.
Don't forget to import the driver that you are ultimately using with a blank import in your executable.

For the list of options to configure sql datasources, refer to the [Fabric Smart Client](https://github.com/hyperledger-labs/fabric-smart-client/) documentation.

## SQL Implementation and Data Criticality

Panurus stores data in several SQL tables. Understanding which tables are "source of truth" and which can be reconstructed from the ledger is vital for disaster recovery and migration planning.

### Table Schema Overview

| Store | Table Name | Primary Key | Description | Criticality |
| :--- | :--- | :--- | :--- | :--- |
| **Keystore** | `key_store` | `key` | Local private keys and secrets. | **Critical** |
| **Identity** | `id_cfgs` | `id, type, url` | Wallet and identity configurations. | **Critical** |
| | `id_info` | `identity_hash` | Audit info and metadata for identities. | **Critical** |
| | `id_signers` | `identity_hash` | Signer information for identities. | **Critical** |
| **Wallet** | `wallets` | `identity_hash, wallet_id, role_id` | Mapping of identities to wallets. | **Critical** |
| **Token** | `tokens` | `tx_id, idx` | Unspent and spent token records. | Recoverable |
| | `tkn_own` | `tx_id, idx, wallet_id` | Ownership relationship for tokens. | Recoverable |
| | `public_params` | `raw_hash` | Cached public parameters of the system. | Recoverable |
| | `tkn_crts` | `tx_id, idx` | Token certifications (for privacy drivers). | Recoverable |
| **TTX** | `requests` | `tx_id` | Full token requests and their statuses. | **Semi-Critical** |
| | `txs` | `id` (UUID) | Granular transaction records. | Recoverable |
| | `movements` | `id` (UUID) | Granular movement records (per enrollment ID). | Recoverable |
| | `req_vals` | `tx_id` | Validation metadata for requests. | Recoverable |
| | `tx_ends` | `id` (UUID) | Endorsement acknowledgments. | Recoverable |
| **Lock** | `tkn_locks` | `tx_id, idx` | Temporary locks for pending transactions. | Transient |

### Token Query Paths and Indexes

The `tokens` table is the one read on the hot path: the token selector queries a wallet's
spendable tokens on every transfer, and re-queries them on every retry. Its indexes are
therefore shaped around the predicates those queries use.

| Index | Columns | Partial predicate | Serves |
| :--- | :--- | :--- | :--- |
| `idx_spent_<t>` | `is_deleted, owner` | — | Owned/spent sweeps. |
| `idx_ski_cleanup_<t>` | `is_deleted, spent_at` | — | Keystore cleanup of deleted tokens. |
| `idx_owner_wallet_id_<t>` | `owner_wallet_id` | — | Lookups by owning wallet. |
| `idx_owner_wallet_part_<t>` | `owner_wallet_id, token_type` | `is_deleted = false AND owner = true` | Unspent tokens of a wallet and type. |
| `idx_issued_<t>` | `redeemed, token_type` | `issuer = true` | Issued/redeemed balances. |
| `idx_spendable_amount_<t>` | `owner_wallet_id, token_type, amount` | `is_deleted = false AND owner = true AND spendable = true` | Spendable tokens of a wallet and type, by amount. |

`amount` is a `NUMERIC(78, 0)` mirror of the authoritative hex `quantity`, maintained on
write by `StoreToken`. `idx_spendable_amount_<t>` makes it usable as a range: with equality on
`owner_wallet_id` and `token_type`, an amount range and an ordering by amount are both
satisfied from the index, without a sort step.

#### Bounded spendable queries

`TokenStore` offers two ways to read a wallet's spendable tokens:

* `SpendableTokensIteratorBy(ctx, walletID, tokenType)` returns **all** of them, unordered and
  unlimited. This is what the selector uses: it shuffles the rows to spread contention across
  concurrent selections, so an order imposed by the database would be discarded.
* `QuerySpendableTokens(ctx, params)` takes a `SpendableTokensQuery` and adds amount bounds, an
  optional ordering and an optional limit, for a caller that needs only part of the set. The
  zero value is equivalent to `SpendableTokensIteratorBy` with an empty wallet and type.

```go
// The three largest spendable TST tokens of alice's wallet worth at least 100.
it, err := store.QuerySpendableTokens(ctx, driver.SpendableTokensQuery{
    WalletID:  "alice",
    TokenType: "TST",
    MinAmount: big.NewInt(100),
    Order:     driver.AmountDescending,
    Limit:     3,
})
```

`MinAmount` and `MaxAmount` are inclusive, and are also available on
`QueryTokenDetailsParams`, so `QueryTokenDetails` can be restricted to an amount range.
`Balance` takes only a wallet and a type and so exposes no bounds, though the query it builds
honours them if a future caller of the internal balance path sets them.

Two properties are worth keeping in mind:

* **Ties are unordered.** Ordering is by `amount` alone, so that the index satisfies the sort.
  Tokens of equal amount are returned in unspecified order, which makes `Limit` a window
  rather than a page — there is deliberately no `Offset`, since offset pagination over
  unstable ties would skip and repeat rows.
* **SQLite is exact only up to `int64`.** SQLite gives a `NUMERIC` column NUMERIC affinity and
  converts an integer literal wider than `int64` to `REAL`. Amount comparisons on SQLite are
  therefore approximate beyond `int64`, which is the same limit that already applies to the
  values stored in the column. Postgres keeps full `NUMERIC(78, 0)` precision.

### Data Criticality Analysis

#### 1. Critical Data (Cannot be lost)
*   **Keys and Secrets (`Keystore`)**: If the private keys stored here are lost and not backed up elsewhere (e.g., in an HSM), any tokens owned by those keys become **permanently unspendable**.
*   **Identity and Wallet Metadata**: These tables contain the "glue" that connects cryptographic identities to user-friendly wallet IDs and provides the audit info required to prove ownership (especially in privacy-preserving drivers like ZKATDLog). Without this, Panurus might not be able to identify which tokens on the ledger belong to which local wallet.

#### 2. Recoverable Data (Can be reconstructed)
*   **Token and Transaction Records**: Most data in `tokendb` and `ttxdb` is derived from the ledger. If the local database is lost but the keys are preserved, Panurus can perform a **Vault Rescan**. During a rescan, Panurus iterates through the ledger history, uses the local keys to identify relevant transactions, and repopulates the local tables.
*   **Public Parameters**: These are typically broadcast on the ledger or provided by the network configuration.

#### 3. Semi-Critical Data
*   **Requests Metadata**: While the core transaction is on the ledger, the `requests` table may contain `application_metadata` (custom JSON provided by the app) that is not always stored on-chain. If your application relies on this local-only metadata, it must be backed up.
## Backend-specific behaviour of the SQL stores

The stores under [`token/services/storage/db/sql/common`](./../../token/services/storage/db/sql/common)
are backend-independent: they build a statement with the query DSL and run it
through a read handle and a write handle. The two SQL backends,
[`postgres`](./../../token/services/storage/db/sql/postgres) and
[`sqlite`](./../../token/services/storage/db/sql/sqlite), supply those handles and
a condition interpreter. The `memory` backend is the SQLite one pointed at
`file::memory:?cache=shared`, not a separate implementation, so everything below
about SQLite applies to it too.

Two things differ per backend and are worth knowing before adding a store.

### The write handle may be decorated (`common.WriteDB`)

A store takes its write handle as `common.WriteDB` rather than `*sql.DB`, which
lets a backend wrap the write path. `*sql.DB` satisfies the interface, so
Postgres passes its pool straight through.

SQLite does not. Its write pool is opened with `maxOpenConns=1` and
`busy_timeout=5000`, so a statement that still comes back `SQLITE_BUSY` was
blocked by a *different* connection — another store on the same file, or another
store's pool on the shared-cache in-memory database — for more than five
seconds. `sqlite.NewBusyRetryWriteDB` wraps the pool so such a statement is
retried a bounded number of times with exponential, jittered backoff instead of
surfacing to the caller. Every SQLite store constructor wraps its handle; a test
asserts that, so a new store that forgets to will fail.

Only `Exec` and `ExecContext` are retried. `Begin` and `BeginTx` return a raw
`*sql.Tx` whose statements go straight to the driver, because retrying one
statement of a transaction is not meaningful — the whole transaction has to be
replayed, and only the caller can decide to do that.

Two consequences for new code:

* Take `common.WriteDB`, not `*sql.DB`, in a new store. If you need a dedicated
  connection (as Postgres advisory locks do), use its `Conn` method; do not
  reach for the concrete pool.
* If a SQLite code path must tolerate write contention and needs a transaction,
  handle the retry at the level of the whole transaction.

### Time offsets are rendered by the interpreter

`cond.OlderThan` and friends do not bind a timestamp computed in Go; they render
an expression the database evaluates, so the comparison uses the database's
clock. Each interpreter renders it in its own dialect:

| Offset | Postgres | SQLite |
| :--- | :--- | :--- |
| none | `NOW()` | `datetime('now')` |
| whole seconds | `NOW() - INTERVAL '5 seconds'` | `datetime('now', '-5 seconds')` |
| fractional | `NOW() - INTERVAL '0.5 seconds'` | `strftime('%Y-%m-%d %H:%M:%f', 'now', '-0.5 seconds')` |

SQLite needs the third row because `datetime()` formats its result to whole
seconds and would drop the fraction even though it accepts one in the modifier.
Both SQLite forms produce a string that orders correctly against a stored
timestamp, which is what the comparison relies on: SQLite compares them as text.

Timestamp columns are declared `TIMESTAMPTZ` so that on Postgres both sides of
such a comparison are timezone-consistent.

### Postgres notifications are a hint, not a log

`postgres.Notifier` turns row changes into callbacks via a trigger, `pg_notify`
and `LISTEN`. It is a latency optimisation, not a change log, and a subscriber
must be written accordingly:

* **The callback runs inline on the listener goroutine.** One goroutine owns the
  channel's connection, reads notifications one at a time and calls every
  subscriber in turn. A callback that blocks stalls delivery of every later
  notification on that table, for every subscriber, and must not call back into
  the notifier. Hand slow work to a goroutine or a queue.
* **Notifications can be missed, so re-read rather than trust the payload.**
  Postgres queues a notification only for sessions already listening and never
  replays it. The first `Subscribe` installs the trigger and then waits for the
  listener to register `LISTEN`, so a row written after it returns is delivered;
  but rows written before that point — by another process, or by this one during
  startup — are not. A dropped connection opens the same window again while it is
  re-established; `TransportError` reports those. Treat a notification as "this
  table changed, go look", which is what `identity/membership`'s subscriber
  already does.

## Store Contract Notes

These rules are part of the store interfaces, not of any one backend. A custom
store implementation must honour them; the SQL stores under
`token/services/storage/db/sql/common` are the reference implementation.

### Existence checks distinguish "absent" from "unknown"

`WalletStoreService.IdentityExists` returns `(bool, error)`. A non-nil error
means the lookup itself failed and the answer is unknown — it must not be
reported as `false`, which would make a transient connectivity problem
indistinguishable from a genuine non-membership.

Callers that cannot carry an error (`Registry.ContainsIdentity`, which backs the
public `driver.Wallet.Contains`) log the error before collapsing it to `false`.

### Idempotent writes

Several writes are replayed during normal operation — a node restart, a retried
view, a second replica handling the same request — and must succeed rather than
report a conflict:

*   `WalletStore.StoreIdentity` and `TokenStore.StorePublicParams` insert with
    `ON CONFLICT DO NOTHING`. A read followed by a write is not sufficient on its
    own: two callers can both observe "not present" and then race the insert, so
    without the conflict clause one of them surfaces a raw constraint violation.
    `StorePublicParams` does still read first, but for a different reason — the
    read is what detects a stored row whose `raw` and `raw_hash` disagree, which
    the insert would otherwise turn into an opaque primary-key error.
*   `KeystoreStore.Put` is idempotent only for a byte-identical value. Storing a
    *different* value under an existing key is a genuine data-integrity conflict
    and must be returned as an error, never silently accepted or ignored.

### Spendable-flag reconciliation

`TokenStoreTransaction.SetSpendableBySupportedTokenFormats` reconciles every
token's `spendable` flag against the set of ledger formats the node supports:
tokens with a supported format become spendable, all others become
non-spendable. An empty format list therefore makes *nothing* spendable.

Implementations should only write rows whose flag actually has to change. The
call runs on every format reconciliation, so clearing the whole table before
re-marking the supported rows costs a full-table rewrite — and the attendant
MVCC bloat on PostgreSQL — however little has changed.

### Driver errors are classified by code, never by message text

A store that maps a backend failure onto a meaningful sentinel error — say
`ttxDBError` turning a foreign-key violation into
`driver.ErrTokenRequestDoesNotExist` — must decide from the code the driver
reports through its own typed error, never from the text of the message.
Message wording is not part of any driver's contract and has changed across both
PostgreSQL and SQLite releases, so a match on it breaks silently at the next
dependency bump.

`token/services/storage/db/sql/common/sqlerrors.go` is the single place that does
this classification. It matches drivers through the narrow interfaces they
already satisfy — `SQLState() string` for `*pgconn.PgError`, `Code() int` for
modernc.org/sqlite's `*sqlite.Error` — so this technology-agnostic layer takes no
driver dependency, and it unwraps, so wrapping an error on the way up preserves
the classification.

A driver that exposes neither accessor is deliberately left unclassified: every
predicate reports `false` rather than falling back to a message match. Each
caller's `false` branch is the conservative one — the original error is returned
unmapped, and a cached prepared statement is left in place — whereas guessing
from message text risks mapping an unrelated failure onto a specific error that
callers act on.

Only codes a real backend can actually return belong in the table. A code the
driver resolves for itself is not a defensive extra branch, it is a branch no
deployment can reach: `SQLITE_SCHEMA` is left out for exactly this reason, since
`modernc.org/sqlite` prepares through `sqlite3_prepare_v2` and SQLite
re-prepares the statement transparently instead of returning the code.

FSC's per-driver `ErrorMapper` (`driver.SQLErrorWrapper`) classifies by code the
same way, and stores already inject it to recognise
`driver.UniqueKeyViolation`. The foreign-key table in `sqlerrors.go` is local only
because FSC has no `ForeignKeyViolation` sentinel to map onto yet; when it gains
one, prefer the injected wrapper over a second table here.

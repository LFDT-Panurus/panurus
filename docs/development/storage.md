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

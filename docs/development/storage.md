# Storage

For an introduction into the concepts of Database, Persistence, Driver, Store, read [this documentation](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/db-driver.md).

SQL is not written by hand in these layers: the stores build it with the query-builder
DSL documented in [SQL Query DSL](./sql-query-dsl.md), which also covers the pagination
strategies and their trade-offs.

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
`QueryTokenDetailsParams` so that `QueryTokenDetails` and `Balance` can be restricted to an
amount range.

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
# Storage

For an introduction into the concepts of Database, Persistence, Driver, Store, read [this documentation](https://github.com/hyperledger-labs/fabric-smart-client/blob/main/docs/platform/view/db-driver.md).

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
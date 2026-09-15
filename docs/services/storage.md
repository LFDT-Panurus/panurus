# Storage Service

The **Storage Service** (`token/services/storage`) encapsulates all data persistence mechanisms required by Panurus. It provides a robust, SQL-based management system to ensure that token states, transaction history, and cryptographic identities are securely tracked and retrievable.

## Architecture

The storage layer is built on a provider-based architecture that supports multiple SQL backends, primarily **SQLite** (for local development and edge nodes) and **PostgreSQL** (for production-grade scalability).

![Database Schema](../imgs/storage_db.png)

## Schema Description

Panurus storage is organized into logical databases, each serving a specific role in the transaction lifecycle and identity management.

### Amount Storage Strategy

**All token amounts** across the storage layer are stored as `NUMERIC(78, 0)` in the database, supporting arbitrary precision integers up to 78 digits. This design choice enables:
- Representation of token amounts exceeding the uint64 maximum value (2^64-1 ≈ 1.8×10^19)
- Support for tokens with high precision or extremely large supply
- Consistent handling of amounts across all tables (Tokens, Transactions, Movements)

Panurus uses Go's `*big.Int` type throughout the codebase to handle these large values, with automatic conversion between database NUMERIC values and in-memory big.Int representations via the `BigInt` scanner type.

### Transaction & Audit Store (TTXDB / AuditDB)

The [`ttxdb/auditdb`](./storage/ttxdb.md) manages the token request lifecycle and transactions records.

The following tables track the lifecycle of token requests from assembly to finality. 
`AuditDB` uses the same schema but is isolated for compliance reporting.

*   **Requests**: Tracks high-level token request state. Contains marshaled requests, current status (Pending, Confirmed, Deleted, Orphan), application/public metadata, and the `recovery_claimed_by`/`recovery_claim_expires_at` lease columns used by the Transaction Recovery Service to atomically claim batches of pending transactions.
*   **Transactions**: Records individual actions (Issue, Transfer, Redeem) within a request, including sender/recipient IDs and amounts (stored as `NUMERIC(78, 0)`).
*   **Movements**: Aggregates net value changes per enrollment ID (amounts stored as `NUMERIC(78, 0)`). Used to efficiently calculate balances and history.
*   **Endorsements**: Collects digital signatures from participants and auditors required for transaction finality.

### Endorser Store (EndorserDB)
The [`endorserdb`](./storage/endorserdb.md) manages validation records created during the token request endorsement process. It shares the physical database with TTXDB but owns its own, self-contained table — it does not write to the Requests table.

*   **Validations**: Stores cryptographic validation metadata produced during the request verification phase. The table is self-contained: it stores the token request data, metadata, and pp_hash directly (along with its own `status`/`status_message` columns), so a validation record can be created and its status tracked without any foreign key into the Requests table. This lets non-owner (endorser) nodes track and recover transactions independently of the TTXDB/AuditDB Requests row for the same `tx_id`.

### Token Store (TokenDB)
This store serves as the authoritative registry for all tokens (UTXOs) known to the node.

*   **Tokens**: The core UTXO registry. Stores token identifiers, amounts, types, ownership info, and ledger-specific state.
    *   **Amount Storage**: Token amounts are stored as `NUMERIC(78, 0)` in the database, supporting arbitrary precision integers up to 78 digits. This enables representation of token amounts that exceed the uint64 maximum value (2^64-1 ≈ 1.8×10^19). Panurus uses Go's `*big.Int` type throughout the codebase to handle these large values, with automatic conversion between database NUMERIC values and in-memory big.Int representations.
    *   **Flags**: Each row carries boolean flags marking the token's relationship to this node: `owner` (mine), `auditor` (I audited it), `issuer` (I issued it), and `redeemed` (an empty-owner output attributed to one of my issuer identities). The `issuer`+`redeemed` combination backs the `IssuedBalance`/`RedeemedBalance` queries exposed by `IssuerWallet`; issued rows are never filtered by `is_deleted` since they are a permanent historical record.
        > [!WARNING]
        > **Breaking schema change**: `redeemed` is a new `NOT NULL` column on the `Tokens` table. It is created automatically by `CREATE TABLE IF NOT EXISTS` on fresh installs, but existing databases require a manual `ALTER TABLE ... ADD COLUMN IF NOT EXISTS redeemed BOOL NOT NULL DEFAULT false` migration before upgrading — see [Storage DB Schema Upgradability](../upgradability.md#storage-db-schema-upgradability).
*   **TokenOwners**: A mapping table linking specific tokens to wallet identifiers for fast lookups.
*   **PublicParameters**: A cache for the network's cryptographic public parameters and their hashes.
*   **TokenCertifications**: Stores third-party certifications for tokens, often required by privacy-preserving drivers.
*   **TokenLocks**: Manages short-lived pessimistic locks on tokens to prevent double-spending during transaction assembly.
*   **TokenSKICleanups**: Tracks keystore cleanup operations for deleted tokens. Records when cryptographic keys were removed from the keystore and which instance performed the cleanup, preventing reprocessing and enabling audit trails in multi-instance deployments.

### Wallet & Identity Store (WalletDB / IdentityDB)
Manages the cryptographic identities and logical wallet groupings used by the node.

*   **Wallets**: Maps user identities to logical wallets and specific roles (e.g., Owner, Auditor).
*   **IdentityConfigurations**: Persists long-term configuration data for various identity types (e.g., Idemix, X.509).
*   **IdentityInfo**: Stores sensitive identity-related data, including audit information and PII required for transaction processing.
*   **IdentitySigners**: Tracks which identities have locally available signing keys and their associated metadata.

#### Idempotent identity registration

`RegisterIdentityDescriptor` writes up to four rows in a single transaction: the signer info and
the audit info of the identity, plus the same two for its alias. The operation is **idempotent and
safe to retry**. Registering the same descriptor twice succeeds without error, and a registration
that was only partially persisted — for example because a previous attempt was interrupted by a
crash — is completed by the retry rather than rejected.

Each of those inserts is individually idempotent (a duplicate key means "already registered"), but
they share one transaction, so each one is isolated by its own `SAVEPOINT`. Without it, the first
duplicate key would abort the whole transaction on PostgreSQL and every following statement would
fail with a generic `current transaction is aborted` error (`SQLSTATE 25P02`) that no longer
identifies itself as a unique-key violation. Rolling back to the savepoint clears that state, so
the rows that are still missing are written and only genuine errors abort the registration.

### Generic Store
*   **KeyStore**: A secure, generic key-value store used for persisting various cryptographic materials and small sensitive states. The keystore uses identifiers that are expected to be the hexadecimal representation of the key's Subject Key Identifier (SKI). This convention is relied upon by other packages, particularly for operations like keystore cleanup where SKIs are derived from owner identities to locate and manage keys.

## Table Name Customisation

Each SQL table name is derived from a canonical **short code** (e.g. `id_signers`) that
is wrapped by the FSC formatter with the configured `TablePrefix` and `TableNameParams`.
Two independent options let you control how the final name is composed:

| Option | Config key | Default |
|---|---|---|
| Override individual short codes | `token.storage.tableNames` | _(none)_ |
| Skip the FSC-generated prefix entirely | `token.storage.skipPrefix` | `false` |

### Allowed characters

The `TableNameParams` are the TMS identity — **network**, **channel** and **namespace** —
and they become part of every table name, so they must be reducible to a legal SQL
identifier. The characters that are common in Fabric names but illegal in an identifier
are escaped rather than rejected:

| Character in a param | Becomes |
|---|---|
| `_` | `__` |
| `-` | `_d` |
| `.` | `_f` |

Letters, digits and underscores pass through as-is, so a channel called `channel1` or
`mychannel01` is fine. The **final composed name** — prefix included — must still be a
valid identifier: only letters, digits and underscores, and it cannot **start** with a
digit (unquoted identifiers in both SQLite and PostgreSQL must start with a letter or an
underscore). Since the prefix comes first and can never start with a digit, this only
bites when `skipPrefix` is enabled *and* the network name starts with a digit.

Anything else — a space, `!`, `/`, `;`, … — is a **configuration error**: store
construction returns an error naming the offending value, it does not crash the node.
A bad prefix or param breaks every table name in the same way and is reported once;
invalid [short-code overrides](#overriding-short-codes-tablenames) are per-key, so **all**
of them are reported together and you do not have to fix them one restart at a time.

The `TablePrefix` is stricter: only letters and underscores, at most 100 characters. It is
lower-cased before use.

### Overriding short codes (`tableNames`)

You can replace any short code globally (for all TMS instances on the node) via the
`token.storage.tableNames` key. The override value replaces the short code *before*
the FSC formatter runs, so the final table name still has the FSC-generated prefix and
params applied around it.

**Unknown keys** are warned about in the log and silently ignored — they do not cause an error.

```yaml
token:
  storage:
    tableNames:
      id_signers: identity_signers   # was: fsc_id_signers_<prefix>_<params>
      tokens: my_tokens              # was: fsc_tokens_<prefix>_<params>
```

### Skipping the prefix (`skipPrefix`)

When set to `true`, the FSC-generated prefix is omitted from **all** table names.
This is useful when connecting to an existing database whose tables were created without
a prefix, or when the target database already enforces schema-level isolation.

Default: `false`.

```yaml
token:
  storage:
    skipPrefix: true
```

With `skipPrefix: true` the name pattern changes from `fsc_<short_code>_<params>` to
`<params>_<short_code>`. Short-code overrides from `tableNames` are still respected.

> **Caution:** Enabling `skipPrefix` on a node that previously ran with the default prefix
> will cause the node to look for tables under different names. Ensure the underlying
> tables already exist under the unprefixed names before enabling this flag.

Both options can be combined:

```yaml
token:
  storage:
    skipPrefix: true
    tableNames:
      tokens: my_tokens   # final name: <params>_my_tokens  (no prefix)
```

### Short Code Reference

The table below lists every short code, the `TableNames` struct field it controls,
and the default table name pattern it produces (before any override is applied).

| Short Code | `TableNames` field | Default pattern |
|---|---|---|
| `movements` | `Movements` | `fsc_movements_<prefix>_<params>` |
| `txs` | `Transactions` | `fsc_txs_<prefix>_<params>` |
| `tx_ends` | `TransactionEndorseAck` | `fsc_tx_ends_<prefix>_<params>` |
| `requests` | `Requests` | `fsc_requests_<prefix>_<params>` |
| `req_vals` | `Validations` | `fsc_req_vals_<prefix>_<params>` |
| `tokens` | `Tokens` | `fsc_tokens_<prefix>_<params>` |
| `tkn_own` | `Ownership` | `fsc_tkn_own_<prefix>_<params>` |
| `tkn_crts` | `Certifications` | `fsc_tkn_crts_<prefix>_<params>` |
| `tkn_locks` | `TokenLocks` | `fsc_tkn_locks_<prefix>_<params>` |
| `public_params` | `PublicParams` | `fsc_public_params_<prefix>_<params>` |
| `wallets` | `Wallets` | `fsc_wallets_<prefix>_<params>` |
| `id_cfgs` | `IdentityConfigurations` | `fsc_id_cfgs_<prefix>_<params>` |
| `id_info` | `IdentityInfo` | `fsc_id_info_<prefix>_<params>` |
| `id_signers` | `Signers` | `fsc_id_signers_<prefix>_<params>` |
| `key_store` | `KeyStore` | `fsc_key_store_<prefix>_<params>` |
| `eid_leases` | `EIDLeases` | `fsc_eid_leases_<prefix>_<params>` |
| `tkn_ski_cleanups` | `TokenSKICleanups` | `fsc_tkn_ski_cleanups_<prefix>_<params>` |

> **Note:** When no `TablePrefix` is configured (empty string) and no `TableNameParams` are
> present, the pattern simplifies to `fsc_<short_code>` (e.g. `fsc_id_signers`).

---

## Internal Databases

Panurus partitions its data into several specialized databases to maintain a clean separation of concerns.

### Transaction Store (TTXDB)
The `ttxdb` serves as the central repository for the lifecycle of token requests. It is used by the **TTX Service** to track:
*   **Requests**: The assembly status of token requests (Pending, Valid, Invalid).
*   **Transactions**: The individual actions (Issue, Transfer) within a request.
*   **Movements**: The flow of value (source to destination) once a transaction is finalized.
*   **Endorsements**: Signatures and acknowledgments received from participants and auditors.

### Endorser Store (EndorserDB)
The `endorserdb` manages validation records for token requests during the endorsement process. It is used by the **Endorsement Service** to:
*   **Validations**: Store validation metadata, token requests, and their own status validated by endorser nodes, in a single self-contained table.

The endorserdb shares the same physical database as ttxdb but owns its own table and interface for validation-specific operations, improving modularity and separation of concerns.

### Token Store (TokenDB)
The `tokendb` is the registry for the current state of all tokens (UTXOs) known to the node. It is used by the **Selector Service** and **Vault Service** to:
*   **Tokens**: Track unique identifiers, types, quantities, and current owners.
*   **Token Locks**: Manage temporary locks on tokens during transaction assembly to prevent double-spending.
*   **Public Parameters**: Store the cryptographic public parameters discovered via the Network Service.

### Audit Transactions Store (AuditDB)
For nodes acting in an **Auditor** role, the `auditdb` provides a specialized repository for audit-related records. While its schema is identical to `ttxdb`, it is isolated to ensure that auditing activities do not interfere with standard transaction processing and to support enhanced compliance reporting.

### Wallet and Identity Store (WalletDB)
The `walletdb` and associated stores (IdentityDB, KeyStore) manage the cryptographic identities used by the node. They track:
*   **Identity Configurations**: Long-term configurations for different roles (Owner, Issuer, Auditor).
*   **Wallets**: The mapping between human-readable wallet identifiers and cryptographic identities.
*   **Signer Information**: Metadata indicating which identities have locally available signing keys.
*   **Recipient Data**: Identities of external parties (e.g., counterparties in a transfer) discovered during interactive protocols.

## Data Persistence Strategy

The Storage Service follows a "Finality-Driven" update strategy. While transactions are being assembled, they are stored in a `Pending` state. 
The `TokenDB` and `Movements` tables are typically updated only when the **Network Service** confirms that a transaction has reached finality on the ledger. 
This ensures that the local view of the "Token Landscape" always reflects the ground truth of the distributed ledger.

## Transaction Recovery Service

The Storage Service includes a **Transaction Recovery Service** that provides the core recovery mechanism for handling pending transactions that may have lost their finality listeners due to node restarts, network interruptions, or other failures.

For detailed documentation on the recovery service architecture, configuration, and usage, see [**Transaction Recovery Service**](storage/recovery.md).

### Architecture

The recovery service is instantiated by the **Network Service** (both Fabric and FabricX implementations) and operates on either the `TTXDB` (for regular transactions) or `AuditDB` (for auditor nodes). 
It provides a generic recovery mechanism that is independent of the specific network backend.

### Recovery Manager

The recovery manager runs in the background and periodically scans for pending transactions that are eligible for recovery. 
It uses a distributed locking mechanism (PostgreSQL advisory locks) to ensure only one replica in a multi-instance deployment performs recovery at a time.

**Key Features:**
- **Backend-Agnostic**: The recovery service is part of the storage layer and works with any network backend (Fabric, FabricX)
- **Database-Driven**: Operates on `TTXDB` for regular nodes and `AuditDB` for auditor nodes
- **Multi-Database Support**:
  - **PostgreSQL**: Recommended for production multi-instance deployments. Uses advisory locks for distributed coordination and leader election
  - **SQLite**: Supported for single-node deployments and development. Handles node restarts gracefully but is not designed for multi-replica scenarios
- **Configurable Behavior**: Recovery parameters can be tuned via configuration (see [Configuration](../configuration.md), Section `Optional: token.tms.<name>.services.network.fabric.recovery`)

### Recovery Process

The recovery service follows this workflow:

1. **Scan Phase**: The recovery manager periodically scans the transaction database for pending transactions older than the configured TTL. The claim query reads only the minimum projection needed (`tx_id`, `stored_at`) and returns a lightweight `RecoveryClaim` for each row, avoiding the cost of materialising the full transaction record on every sweep.
2. **Claim Phase**: Eligible transactions are claimed by the current instance using a lease mechanism. On PostgreSQL this is an atomic `UPDATE ... RETURNING`; SQLite uses a non-atomic but functionally equivalent permissive claim.
3. **Recovery Phase**: For each claimed transaction, the recovery handler:
   - Queries the network for the transaction's current status
   - Based on the status (Valid, Invalid, NotFound, or Busy), applies the appropriate finality logic
   - For valid transactions, verifies the token request hash and commits to the local database
   - For invalid transactions, marks them as `Deleted`
   - For transactions whose status keeps returning `NotFound` past the configured `notFoundGracePeriod`, marks them as `Orphan`. This indicates the transaction never reached the ledger (e.g. broadcast failure, mempool drop) and prevents long-stuck rows from blocking the head of the recovery queue, while keeping them distinguishable from ledger-rejected transactions that are marked `Deleted`.
   - For busy (pending) transactions, returns an error and the transaction remains eligible for future recovery
4. **Lease Management**: Claimed transactions are leased to the instance for a configured duration to prevent stuck transactions from blocking recovery

### Integration with Network Service

The Network Service (Fabric and FabricX implementations) instantiates the recovery service during initialization:
- Creates a recovery manager with the appropriate transaction database (TTXDB or AuditDB)
- Configures the recovery handler with network-specific transaction status query capabilities
- Starts the background recovery process

This design ensures that recovery is handled consistently across different network backends while maintaining the separation of concerns between storage and network layers.

### Database Backend Considerations

The recovery service supports both PostgreSQL and SQLite backends, with different characteristics:

**PostgreSQL (Recommended for Production):**
- **Multi-Instance Support**: Uses advisory locks for distributed coordination
- **Leader Election**: Only one replica performs recovery sweeps at a time
- **High Availability**: Multiple replicas can share the same database
- **Scalability**: Handles high transaction volumes efficiently

**SQLite (Development & Single-Node Deployments):**
- **Single-Node Only**: Designed for scenarios where only one node accesses the database
- **Node Restart Support**: Handles node restarts gracefully by recovering pending transactions on startup
- **Simplicity**: No distributed coordination overhead
- **Limitations**: Not suitable for multi-replica deployments as SQLite does not support the advisory lock mechanism used for leader election

**Important**: When using SQLite, ensure that only one node instance accesses the database file. Running multiple replicas with SQLite will result in undefined behavior and potential data corruption.

### Configuration

Recovery behavior is controlled by the `token.tms.<name>.services.network.fabric.recovery` configuration section. 

## Keystore Cleanup Service

The Storage Service includes a **Keystore Cleanup Service** that provides automatic deletion of cryptographic keys from the keystore for tokens that have been deleted (spent, expired, or invalidated). This ensures that the keystore doesn't accumulate stale keys indefinitely, improving security and reducing storage overhead.

For detailed documentation on the cleanup service architecture, configuration, and usage, see [**Keystore Cleanup Service**](storage/keystore_cleanup.md).

### Architecture

The cleanup service operates on the token database and keystore, scanning for deleted tokens that are eligible for key cleanup. It uses a distributed locking mechanism (PostgreSQL advisory locks) to ensure only one replica in a multi-instance deployment performs cleanup at a time.

### Cleanup Manager

The cleanup manager runs in the background and periodically scans for deleted tokens whose cryptographic keys can be safely removed from the keystore.

**Key Features:**
- **Automatic Key Deletion**: Removes keys for deleted tokens after a configurable TTL period
- **SKI Derivation**: Derives Subject Key Identifiers (SKIs) from owner identities to locate keys
- **Multi-Database Support**:
  - **PostgreSQL**: Recommended for production multi-instance deployments. Uses advisory locks for distributed coordination and leader election
  - **SQLite**: Supported for single-node deployments and development. Handles node restarts gracefully but is not designed for multi-replica scenarios
- **Configurable Behavior**: Cleanup parameters can be tuned via configuration (see [Configuration](../configuration.md))

### Cleanup Process

The cleanup service follows this workflow:

1. **Leadership Acquisition**: The cleanup manager acquires an advisory lock to become the leader
2. **Scan Phase**: Scans the token database for deleted tokens older than the configured TTL (default: 24 hours) that haven't had their keys cleaned
3. **SKI Derivation**: For each eligible token, derives SKIs from the owner identity
4. **Key Deletion**: Deletes the derived keys from the keystore using parallel workers
5. **Tracking**: Marks tokens as cleaned in the database to prevent reprocessing

### Database Backend Considerations

The cleanup service supports both PostgreSQL and SQLite backends, with different characteristics:

**PostgreSQL:**
- **Multi-Instance Support**: Uses advisory locks for distributed coordination
- **Leader Election**: Only one replica performs cleanup sweeps at a time
- **High Availability**: Multiple replicas can share the same database

**SQLite:**
- **Single-Node Only**: Suitable for development and single-node deployments
- **Node Restart Support**: Cleanup resumes automatically after restart
- **No Multi-Replica Support**: Lacks advisory lock mechanism for leader election

### Configuration

Cleanup behavior is controlled by the configuration section. See the [Configuration Guide](../configuration.md) for detailed parameter descriptions and tuning recommendations.

See the [Configuration Guide](../configuration.md), Section `Optional: token.tms.<name>.services.network.fabric.recovery`, for detailed parameter descriptions and tuning recommendations.

## Storage API Limits

The storage service limits its writes and reads so one huge write or one runaway
query can't exhaust the database (issue #1630, CWE-400 / CWE-770). The limits are
enforced by a single, stackable **guard** decorator layer
(`token/services/storage/db/guard/`) applied once at the multiplexed driver
(`token/services/storage/db/multiplexed/driver.go`) as each store is created, so
every backing driver (SQLite, PostgreSQL) and every store is covered by the same
mechanism. The decorators embed the store interfaces and override only the methods
that need a check, delegating everything else — new layers (metrics, tracing) can be
stacked the same way, with the concrete SQL store at the bottom.

A single `Policy` (`MaxPayloadSize`, `MaxPageSize`) drives every check. It is loaded
once from configuration; absent keys fall back to the built-in defaults, and an
explicit `0` disables that check.

**Write size limit.** Writes whose serialised payload exceeds `maxPayloadSize`
(default 4 MiB; `0` disables) are rejected before reaching the database. Covered
writes include the transaction store's `AddTokenRequest` / `AddTransaction` /
`AddMovement` / `AddTransactionEndorsementAck`, the token store's `StoreToken` /
`StorePublicParams` / `StoreCertifications`, the endorser store's
`AddValidationRecord`, the identity store's `StoreIdentityData` / `StoreSignerInfo` /
`RegisterIdentityDescriptor` / `AddConfiguration`, and the wallet store's
`StoreIdentity`.

**Read size limit.** `maxPageSize` (default 1000) bounds the paginated reads —
currently `QueryTransactions` on the owner and audit transaction stores. Those
reads require a bounded page: `nil` and `pagination.None()` are rejected, as is a
page size larger than `maxPageSize`. Callers page through the full result set.

Rejecting the request is only a safe way to bound a read when the caller has a way
to comply, so the cap is applied exactly to the reads that accept a pagination
argument. Streaming iterator reads are **not** row-capped:

- The token store's unspent/spendable/unsupported iterators, the endorser store's
  `QueryValidations`, the transaction store's `QueryTokenRequests`, and the identity
  store's `IteratorConfigurations` all take no page size or cursor, and their
  consumers drain them in full — the selectors and integrity checks for the token
  iterators, and `LocalMembership.storedIdentityConfigurations` on the identity
  `Load` path for `IteratorConfigurations`. Capping any of them would turn a large
  but legitimate dataset into a hard failure that the caller cannot page around.
- `QueryMovements` is uncapped for a second reason: it feeds balance totals, so
  dropping rows would quietly return wrong balances. It is already narrowed by its
  required filters.

You can override the limits in configuration (`token.storage.maxPayloadSize` /
`token.storage.maxPageSize`) — see the [Configuration Guide](../configuration.md).

**Not yet covered (tracked follow-up).** Two groups of reads cannot be bounded by a
wrapper at all, and need query-level work instead:

- The streaming iterators listed above. Bounding them requires a SQL-level `LIMIT`
  on the query (with a defined order and documented truncation) or adding paging to
  their signatures, so the caller can ask for the next page rather than fail.
- Reads that materialise a full slice or map before returning (e.g.
  `ListUnspentTokens`, `QueryTokenDetails`, `ConfigurationsByID`, `GetWalletIDs`) —
  the SQL has already loaded everything by the time the decorator sees the result.

The opaque `Keystore.Put` value and input-size caps for variadic id lists
(`DeleteTokens`, `GetTokens`) are in the same follow-up.

## Ledger Drift Checks

The Storage Service includes **Ledger Drift Checks**, a background sweep that compares what the node has stored against what the ledger says and reports every place the two disagree.

For detailed documentation on the checks, their findings, configuration and metrics, see [**Ledger Drift Checks**](storage/checks.md).

### Architecture

The sweep is started by the check service provider, which the auditor and owner services already ask for a check service once per TMS. A node therefore runs one sweep per store it owns: `owner` over the `TTXDB` and `auditor` over the `AuditDB`. Like recovery and cleanup it elects a leader through a PostgreSQL advisory lock, on a lock id of its own so the sweeps do not wait on each other.

### What It Checks

- **Transaction Check**: the status of every locally stored transaction against the ledger's
- **Unspent Tokens Check**: the content of every locally held unspent token against the ledger's copy
- **Token Spendability Check**: whether every locally held unspent token can still be spent by the current TMS
- **Local Completeness Check**: the outputs every confirmed transaction should have written locally against what is actually stored. This is the ledger-to-local direction the other checks cannot see, and the one that costs the owner money when it goes wrong

The first three are also what the on-demand `Check` call on the auditor and owner services runs. The fourth rebuilds every confirmed transaction, so it runs in the background sweep only.

### Findings

Each divergence is recorded as a finding with a stable key, a code and a severity, in a findings table on the transaction store. A sweep ages a finding it sees again rather than storing it twice, and closes one it stops seeing, so the open findings are always the current state rather than a growing log. A check that could not complete never closes its own findings, so an unreachable ledger cannot be mistaken for a clean bill of health.

### Configuration

Drift check behavior is controlled by the `token.tms.<name>.services.storage.checks` configuration section. See [**Ledger Drift Checks**](storage/checks.md) for the parameters.

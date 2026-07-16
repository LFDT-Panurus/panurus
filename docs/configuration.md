# Panurus Configuration Example

The following example provides descriptions for the various keys required by Panurus.

```yaml
# ------------------- Panurus Configuration -------------------------
token:
  # version is the version of this configuration structure. 
  # If not specified, the latest version is used.
  version: v1
  # enabled determines if Panurus is enabled.
  enabled: true

  # selector configuration allows the use of different implementations of the token selector.
  # The "sherdlock" driver is the default implementation; other possible configurations are: "simple".
  # If empty, the default selector is used.
  selector:
    driver: sherdlock
    # tokens might be locked because of an ongoing transaction from the same wallet. Instead of failing immediately, the selector can retry.
    # The interval is the exact amount of seconds (for simple selector) or the max amount of seconds (sherdlock) the selector waits before retrying.
    # User-defined default: 5s
    retryInterval: 5s
    # numRetries is the number of times to retry gaining a lock on tokens before failing the transaction.
    numRetries: 3
    # leaseExpiry is the period a token can be locked, after which it is forcefully unlocked.
    # If leaseExpiry is zero, the eviction algorithm is never executed.
    leaseExpiry: 3m
    # leaseCleanupTickPeriod defines how often the eviction algorithm must be executed.
    # If leaseCleanupTickPeriod is zero, the eviction algorithm is never executed.
    leaseCleanupTickPeriod: 90s
    # Token fetcher cache configuration (sherdlock driver only)
    # The fetcher uses a Ristretto cache to store tokens for efficient retrieval.
    # fetcherCacheSize is the maximum number of tokens to cache. Each token consumes 1 unit of cache cost.
    # If not specified or set to 0, defaults to 100 million (1e8) tokens.
    fetcherCacheSize: 100000000
    # fetcherCacheRefresh is the time interval after which the cache is considered stale and will be refreshed.
    # A hard refresh (blocking) occurs when the cache becomes stale. If not specified or set to 0, defaults to 1 second.
    fetcherCacheRefresh: 1s
    # fetcherCacheMaxQueries is the number of queries after which a soft refresh (non-blocking background update) is triggered.
    # This helps keep the cache fresh without blocking queries. If not specified or set to 0, defaults to 5 queries.
    fetcherCacheMaxQueries: 5

  # When we are interested in knowing when a transaction reaches finality, we subscribe to the Finality Listener Manager for the finality event of that transaction.
  # This configuration specifies the way the manager is instantiated (i.e., how it gets notified about the finality events, how often it checks).
  finality:
    # Only applicable for fabric networks.
    # The manager subscribes to the delivery service and receives all final transactions.
    #   This manager keeps two structures: an LRU cache of recently finalized transactions, and a list of listeners that are waiting for future transactions.
    #   When a new block comes from the delivery service, we store all new transactions in the cache and notify all interested listeners.
    #   When a client subscribes to the manager for a specific transaction, we go through the following steps, which correspond to all possible scenarios with decreasing probability:
    #   a) The transaction reached finality recently, so we perform a lookup. If not found, we proceed to step b.
    #   b) The transaction will reach finality shortly, so we append a listener and wait for a timeout. If the listener reaches timeout, we proceed to step c.
    #   c) The transaction reached finality long ago, so we query the whole ledger for this specific transaction. If the query returns no result, we proceed to step d.
    #   d) The transaction will reach finality at some point beyond the timeout or never, so we return Unknown. Then it is up to the client to either append another listener or accept that the transaction will never reach finality.  
    delivery:
      # mapperParallelism is the number of goroutines that process incoming transactions in parallel. Defaults to 1.
      mapperParallelism: 10
      # blockProcessParallelism is the number of blocks we can process in parallel when they arrive from the delivery service.
      # If the value is <= 1, then blocks are processed sequentially.
      # This is the suggested configuration if we are not sure about the dependencies between blocks.
      # The total go routines processing transactions will be blockProcessParallelism * mapperParallelism.
      blockProcessParallelism: 1
      # lruSize detects how many transactions we should guarantee to keep in our recent cache.
      # If the transaction is not among these elements, we proceed to step b, as described above.
      # If lruSize and lruBuffer are not set, then we will never evict past transactions (the cache will grow infinitely).
      lruSize: 30
      # eviction will happen when the cache size exceeds lruSize + lruBuffer.
      lruBuffer: 15
      # listenerTimeout is the duration to listen when we can't find a transaction in the cache (most probably it is about to become final).
      # We will listen for this amount of time and then we will query the whole ledger, as described in step c.
      # If the timeout is not set, then the listener will never be evicted and we will never proceed to step c.
      # We will wait forever for the transaction to return (as is done for the 'committer' type).
      listenerTimeout: 10s
    # Only applicable for fabricx networks
    # notification: The manager is notified about finality events via a notification service (e.g. for FabricX).
    #   When a new notification arrives, an event is added to a queue for asynchronous processing.
    #   When a client subscribes to the manager for a specific transaction, the transaction joins a pending set
    #   that the shared poller sweeps with batched status queries (see poller below).
    notification:
      # workers is the number of goroutines that process events in parallel. Defaults to 10.
      workers: 10
      # queueSize is the size of the event buffer. Defaults to 1000.
      queueSize: 1000
    # Only applicable for fabricx networks
    # poller: resolves the status of pending transactions with periodic batched committer queries.
    poller:
      # interval is how often the poller sweeps the pending set. Defaults to 1s.
      interval: 1s
      # batchSize is the maximum number of txIDs in one committer status query. Defaults to 2000.
      batchSize: 2000
      # pendingTTL is how long a tx stays pending before its slot is reclaimed.
      # It should exceed the longest caller finality timeout. Defaults to 10m.
      pendingTTL: 10m
  
  # fabricx configuration for FabricX-specific settings
  fabricx:
    # lookup configuration for the lookup service
    lookup:
      # permanent lookup configuration
      permanent:
        # interval is the polling interval for permanent lookups. Defaults to 1m.
        interval: 1m
      # one-time lookup configuration
      once:
        # deadline is the maximum time to wait for a one-time lookup. Defaults to 5m.
        deadline: 5m
        # interval is the polling interval for one-time lookups. Defaults to 2s.
        interval: 2s
        
  # optional global SQL table name overrides (applied to all TMS instances).
  # The value replaces the short code; the FSC-generated prefix and params still wrap it.
  # Unknown keys are warned and ignored. Omit the section to keep all default names.
  storage:
    tableNames:
      # id_signers: identity_signers
      # tokens: my_tokens

  tms:
    mytms: # unique name of this token management system
      network: default # the name of the network this TMS refers to (Fabric, etc.)
      channel: testchannel # the name of the network's channel this TMS refers to, if applicable
      namespace: tns # the name of the channel's namespace this TMS refers to, if applicable

      # sections dedicated to the definition of the storage.
      # Panurus uses multiple databases to keep track of transactions, tokens, identities, and audit records where applicable.
      # These are the available databases:
      # ttxdb: stores records of transactions.
      # tokendb: stores information about the available tokens.
      # auditdb: stores audit records about the audited transactions.
      # identitydb: stores information about wallets and identities.
      # The databases can be instantiated in isolation, a different backend for each db, or with a shared backend, depending on the driver used.
      # In the following example, we have all databases using the same backend but tokendb.

      # optional separate configuration for ttxdb, tokendb, tokenlockdb, auditdb, and identitydb
      # otherwise they default to 'default', if it is defined
      tokendb:
        persistence: my_token_persistence

      services:
        # This section contains network specific configuration
        network:
          # Configuration related to the Fabric network
          fabric:
            # In Fabric, the execution of the token chaincode can be endorsed by any node equipped with
            # a proper endorsement key.
            # Therefore, also FSC nodes equipped with proper endorsement keys can perform the same function.
            # This section is dedicated to the configuration of the endorsement of the token chaincode by
            # other FSC nodes.
            fsc_endorsement:
              # Is this node an endorser? true/false
              endorser: true
              # If this node is an endorser, which Fabric identity should be used to sign the endorsement?
              # If empty, the default identity will be used.
              id:
              # This section is used to set the policy to be used to select the endorsers to contact.
              # Available policies are: `1outn`, `all`, `namespace`. Default policy is `all`.
              #   - `1outn`: contact one random endorser from the list below.
              #   - `all`: contact all the endorsers listed below.
              #   - `namespace`: fetch the real endorsement policy of the token namespace and contact a
              #     random subset of the endorsers below that satisfies it. On Fabric, the policy is
              #     obtained via service discovery; on FabricX, via the query service. Endorsement fails
              #     if the endorsers below cannot satisfy the namespace's policy.
              policy:
                type: 1outn
              # A list of FSC node identifiers that must be contacted to obtain the endorsement.
              endorsers:
              - endorser1
              - endorser2
              - endorser2

            # recovery config controls background re-registration of finality listeners
            # for pending transactions that may have lost their listeners due to node restarts,
            # network interruptions, or other failures.
            # If omitted, the recovery manager uses its built-in defaults.
            recovery:
              # enabled determines whether transaction recovery runs. Default: true.
              # Set to false to disable automatic recovery (not recommended for production).
              enabled: true
              
              # ttl is the minimum age of a pending transaction before it is eligible for recovery. Default: 30s.
              # This prevents the recovery manager from interfering with transactions that are still being
              # actively processed. Increase this value if you have long-running transaction assembly processes.
              # Relationship: Should be greater than your typical transaction assembly time.
              ttl: 30s
              
              # scanInterval is how often the recovery manager scans for pending transactions. Default: 5s.
              # Lower values provide faster recovery but increase database load.
              # Higher values reduce overhead but delay recovery detection.
              # Relationship: Should be less than ttl to ensure timely detection of eligible transactions.
              # Performance impact: Each scan queries the transaction database for pending transactions.
              scanInterval: 5s
              
              # batchSize is the maximum number of pending transactions claimed per scan. Default: 100.
              # Limits the number of transactions processed in a single recovery sweep to prevent
              # overwhelming the system. Increase for high-throughput environments with many pending transactions.
              # Performance impact: Larger batches reduce scan overhead but increase memory usage and processing time per sweep.
              batchSize: 100
              
              # workerCount is the number of local workers that process claimed transactions in parallel. Default: 4.
              # Increase to improve recovery throughput in high-volume scenarios.
              # Decrease to reduce resource consumption on constrained systems.
              # Performance impact: More workers increase CPU and network utilization but improve recovery speed.
              workerCount: 4
              
              # leaseDuration is how long a claimed transaction remains leased to this instance before it can be reclaimed. Default: 30s.
              # This prevents stuck transactions from blocking recovery indefinitely if a worker crashes.
              # Should be longer than the typical time to query and process a single transaction.
              # Relationship: Should be greater than the expected network latency + processing time for transaction status queries.
              leaseDuration: 30s
              
              # advisoryLockID is the PostgreSQL advisory lock identifier used for recovery leader election.
              # This ensures only one replica performs recovery sweeps at a time in multi-instance deployments.
              # Default: 8389190333894887286 (hex: 0x74746b7265636f76, ASCII: "ttkrecov")
              # The default value is derived from the ASCII encoding of "ttkrecov" (Token Transaction Recovery).
              # Only change this if you need to run multiple independent recovery managers on the same database.
              # Note: PostgreSQL advisory locks use 64-bit integers. This value must be unique across your application.
              advisoryLockID: 8389190333894887286
              
              # instanceID identifies this replica as the owner of recovery claims.
              # If empty, a process-local identifier is generated automatically at startup using a UUID.
              # Set this explicitly in containerized environments to maintain consistent identity across restarts.
              # This helps with debugging and tracking which instance processed which transactions.
              instanceID:

              # notFoundGracePeriod is the time after which the recovery loop promotes a
              # transaction whose status query keeps returning NotFound to the terminal
              # Orphan status. Without this, a transaction whose audit log was persisted
              # but whose broadcast never reached the ledger would sit at the head of the
              # `ORDER BY stored_at ASC LIMIT batchSize` claim query forever and prevent
              # newer rows from being scanned. Default: 30m.
              # The promoted row is marked Orphan (not Deleted) so operators can tell
              # broadcast failures apart from ledger-rejected transactions.
              # Set to 0 to disable the promotion; the row stays Pending and is re-claimed
              # on every sweep until it either resolves or an operator intervenes.
              notFoundGracePeriod: 30m

        # storage service configuration
        storage:
          # cleanup config controls automatic deletion of cryptographic keys from the keystore
          # for tokens that have been deleted (spent, expired, or invalidated).
          # If omitted, the cleanup manager uses its built-in defaults (disabled by default).
          cleanup:
            # enabled determines whether keystore cleanup runs. Default: false.
            # Must be explicitly enabled. This is a conservative default to prevent
            # unexpected key deletion in existing deployments.
            enabled: false
            
            # ttl is the minimum age of deleted tokens before their keys are eligible for cleanup. Default: 24h.
            # This ensures tokens are truly finalized before key deletion.
            # Increase this value for additional safety margin in high-latency networks.
            # Relationship: Should be significantly greater than transaction finality time.
            ttl: 24h
            
            # scanInterval is how often the cleanup manager scans for deleted tokens. Default: 1h.
            # Lower values provide faster cleanup but increase database load.
            # Higher values reduce overhead but delay key removal.
            # Relationship: Should be less than ttl to ensure timely cleanup.
            # Performance impact: Each scan queries the token database for deleted tokens.
            scanInterval: 1h
            
            # batchSize is the maximum number of deleted tokens processed per scan. Default: 100.
            # Limits the number of tokens processed in a single cleanup sweep.
            # Increase for high-volume environments with many deleted tokens.
            # Performance impact: Larger batches reduce scan overhead but increase memory usage and processing time per sweep.
            batchSize: 100
            
            # workerCount is the number of local workers that process tokens in parallel. Default: 1.
            # Increase to improve cleanup throughput in high-volume scenarios.
            # Decrease to reduce resource consumption on constrained systems.
            # Performance impact: More workers increase CPU utilization during cleanup sweeps.
            workerCount: 1
            
            # advisoryLockID is the PostgreSQL advisory lock identifier used for cleanup leader election.
            # This ensures only one replica performs cleanup sweeps at a time in multi-instance deployments.
            # Default: 8389190333894887277 (hex: 0x74746b636c65616e, ASCII: "ttkclean")
            # The default value is derived from the ASCII encoding of "ttkclean" (Token Transaction Keystore Cleanup).
            # Only change this if you need to run multiple independent cleanup managers on the same database.
            # Note: PostgreSQL advisory locks use 64-bit integers. This value must be unique across your application.
            advisoryLockID: 8389190333894887277
            
            # instanceID identifies this replica in logs and monitoring.
            # If empty, a unique identifier is generated automatically at startup.
            # Set this explicitly in containerized environments for consistent identity across restarts.
            # This helps with debugging and tracking which instance performed cleanup operations.
            instanceID:

      # auditor-specific settings
      auditor:
        # locker configures the distributed locking strategy for the auditor's
        # enrollment-ID (EID) locks. These locks serialise concurrent access
        # to the same EIDs across replicas when processing audit records.
        locker:
          # backend selects the Locker implementation.
          #   "memory"   – in-process mutex (default, single-replica only)
          #   "postgres" – PostgreSQL lease-table (multi-replica)
          backend: memory
          # postgres section is read only when backend == "postgres".
          postgres:
            # ttl is the lease duration for each EID lock row.
            ttl: 30s
            # acquireBackoff is the wait between retry attempts when a lock is contended.
            acquireBackoff: 100ms
            # acquireDeadline is the total time allowed to acquire all EID locks.
            acquireDeadline: 1m
            # heartbeat is the interval at which held leases are renewed (~TTL/3).
            heartbeat: 10s
            # owner identifies this replica. Auto-generated at startup if empty.
            owner:

      # sections dedicated to the definition of the wallets
      wallets:
        # Default cache size reference that can be used by any wallet that supports caching.
        defaultCacheSize: 3
        # owner wallets
        owners:
        - id: alice # the unique identifier of this wallet. Here is an example of use: `ttx.GetWallet(context, "alice")` 
          default: true # is this the default owner wallet
          # path to the folder containing the cryptographic material associated with the wallet.
          # The content of the folder is driver dependent.
          path:  /path/to/alice-wallet
          # Cache size, in case the wallet supports caching (e.g., idemix-based wallet).
          cacheSize: 3
        - id: alice.id1
          path: /path/to/alice.id1-wallet
        # issuer wallets
        issuers:
          - id: issuer # the unique identifier of this wallet. Here is an example of use: `ttx.GetIssuerWallet(context, "issuer")`
            default: true # is this the default issuer wallet
            # path to the folder containing the cryptographic material associated with the wallet.
            # The content of the folder is driver dependent.
            path: /path/to/issuer-wallet
            # additional options that can be used to instantiate the wallet.
            # options are driver dependent. With `fabtoken` and `dlog` drivers,
            # the following options apply.
            opts:
              BCCSP:
                Default: SW
                SW:
                  Hash: SHA2
                  Security: 256
                # The following only needs to be defined if the BCCSP Default is set to PKCS11.
                # NOTE: in order to use pkcs11, you have to build the application with "go build -tags pkcs11"
                PKCS11:
                  Hash: SHA2
                  Label: null
                  Library: null
                  Pin: null
                  Security: 256
        # auditor wallets
        auditors:
          - id: auditor # the unique identifier of this wallet. Here is an example of use: `ttx.GetAuditorWallet(context, "auditor")`
            default: true # is this the default auditor wallet  
            # path to the folder containing the cryptographic material associated with the wallet.
            # The content of the folder is driver dependent.
            path: /path/to/auditor-wallet
            # additional options that can be used to instantiate the wallet.
            # options are driver dependent. With `fabtoken` and `dlog` drivers,
            # the following options apply
            opts:
              BCCSP:
                Default: SW
                PKCS11:
                  Hash: SHA2
                  Label: null
                  Library: null
                  Pin: null
                  Security: 256
                SW:
                  Hash: SHA2
      # Auditor lock configuration for enrollment ID locking during audit operations
      # These settings control the retry behavior when multiple auditors compete for locks
      auditor:
        lock:
          # maxRetries is the maximum number of retry attempts for lock acquisition
          # Default: 10
          maxRetries: 10
          
          # initialBackoff is the initial backoff delay before the first retry
          # Default: 10ms
          initialBackoff: 10ms
          
          # maxBackoff is the maximum backoff delay between retries
          # Default: 5s
          maxBackoff: 5s
          
          # backoffMultiplier is the exponential backoff multiplier
          # Each retry delay is multiplied by this factor
          # Default: 2.0
          backoffMultiplier: 2.0
          
          # jitterFactor is the randomization factor to prevent thundering herd (0.0 to 1.0)
          # Adds random jitter to break symmetry when multiple auditors retry simultaneously
          # Default: 0.3 (30%)
          jitterFactor: 0.3
                  Security: 256
```

## Minimal Configuration

Panurus can start with the following minimal configuration:

```yaml
token:
  enabled: true
  tms:
    default:
      network: mynetwork
      namespace: mynamespace
```

## Configuration Defaults and Optional Sections

### Required Fields

The following fields are strictly required:

- `network`
- `namespace`

All other configuration sections are optional and use sensible defaults.

### Optional: token.selector

If not specified, the default selector implementation is used.

Default values:
- driver: sherdlock
- retryInterval: 5s
- numRetries: 3
- leaseExpiry: 3m
- leaseCleanupTickPeriod: 90s

---

### Optional: token.finality

Default values:

- delivery.mapperParallelism: 10
- delivery.blockProcessParallelism: 10
- delivery.lruSize: 30
- delivery.lruBuffer: 15
- delivery.listenerTimeout: 10s
- notification.workers: 10
- notification.queueSize: 1000
- poller.interval: 1s
- poller.batchSize: 2000
- poller.pendingTTL: 10m

---

### Optional: token.fabricx.lookup

If not specified, the default configuration is:

```yaml
token:
  fabricx:
    lookup:
      permanent:
        interval: 1m
      once:
        deadline: 5m
        interval: 2s
```

Default values:

- permanent.interval: 1m
- once.deadline: 5m
- once.interval: 2s

---

### Optional: token.tms.<name>.services.network.fabric.recovery

If not specified, the default configuration is:

```yaml
token:
  tms:
    <name>:
      services:
        network:
          fabric:
            recovery:
              enabled: true
              ttl: 30s
              scanInterval: 5s
              batchSize: 100
              workerCount: 4
              leaseDuration: 30s
              advisoryLockID: 8389190333894887286
              instanceID:
              notFoundGracePeriod: 30m
```

Default values:

- enabled: true
- ttl: 30s
- scanInterval: 5s
- batchSize: 100
- workerCount: 4
- leaseDuration: 30s
- advisoryLockID: 8389190333894887286 (`0x74746b7265636f76`)
- instanceID: empty, auto-generated when the recovery manager starts
- notFoundGracePeriod: 30m (set to 0 to disable promotion to Orphan)

**Parameter Relationships and Tuning:**

- **Recovery is enabled by default** to ensure automatic recovery of lost finality listeners.
- **Only pending transactions older than `ttl` are considered for recovery** to avoid interfering with active transaction processing.
- **The manager validates** that `ttl`, `scanInterval`, `batchSize`, `workerCount`, and `leaseDuration` are all greater than zero.
- **`advisoryLockID`** is used to acquire PostgreSQL advisory-lock leadership so that only one replica performs a recovery sweep at a time. The default value (8389190333894887286 or 0x74746b7265636f76) represents the ASCII string "ttkrecov" (Token Transaction Recovery) encoded as a 64-bit integer.
- **`instanceID`** is used as the lease owner identifier for claimed transactions; if omitted, the manager generates a unique UUID automatically at startup.
- **`notFoundGracePeriod`** controls how long a transaction whose status query keeps returning `NotFound` is left in `Pending` before being promoted to the terminal `Orphan` status. This protects the recovery sweep from being permanently blocked by transactions whose audit log was persisted but whose broadcast never reached the ledger. The promoted row is marked `Orphan` rather than `Deleted` so operators can distinguish broadcast failures from ledger-rejected transactions. Raise the default if your network has long catch-up windows after committer/orderer restarts; set it to `0` to disable the promotion entirely.

**Tuning Recommendations:**

1. **For High-Throughput Environments:**
   - Increase `batchSize` to 200-500 to process more transactions per sweep
   - Increase `workerCount` to 8-16 to improve parallel processing
   - Decrease `scanInterval` to 2-3s for faster recovery detection


### Optional: token.storage.tableNames

Globally overrides individual SQL table short codes for all TMS instances on the node.
The override value replaces the short code *before* the FSC formatter runs, so the
FSC-generated prefix and params are still applied around it. Unknown keys are warned
and ignored.

If not specified (or the section is omitted entirely), all tables use their default names.

```yaml
token:
  storage:
    tableNames:
      id_signers: identity_signers   # replaces the "id_signers" short code
      tokens: my_tokens              # replaces the "tokens" short code
```

All 17 configurable short codes and their defaults:

| Short code | Default table name pattern |
|---|---|
| `movements` | `fsc_movements_<prefix>_<params>` |
| `txs` | `fsc_txs_<prefix>_<params>` |
| `tx_ends` | `fsc_tx_ends_<prefix>_<params>` |
| `requests` | `fsc_requests_<prefix>_<params>` |
| `req_vals` | `fsc_req_vals_<prefix>_<params>` |
| `tokens` | `fsc_tokens_<prefix>_<params>` |
| `tkn_own` | `fsc_tkn_own_<prefix>_<params>` |
| `tkn_crts` | `fsc_tkn_crts_<prefix>_<params>` |
| `tkn_locks` | `fsc_tkn_locks_<prefix>_<params>` |
| `public_params` | `fsc_public_params_<prefix>_<params>` |
| `wallets` | `fsc_wallets_<prefix>_<params>` |
| `id_cfgs` | `fsc_id_cfgs_<prefix>_<params>` |
| `id_info` | `fsc_id_info_<prefix>_<params>` |
| `id_signers` | `fsc_id_signers_<prefix>_<params>` |
| `key_store` | `fsc_key_store_<prefix>_<params>` |
| `eid_leases` | `fsc_eid_leases_<prefix>_<params>` |
| `tkn_ski_cleanups` | `fsc_tkn_ski_cleanups_<prefix>_<params>` |

> **Note:** When no `TablePrefix` is configured and no `TableNameParams` are present, the
> pattern simplifies to `fsc_<short_code>` (e.g. `fsc_id_signers`).

---

### Optional: token.storage.skipPrefix

When set to `true`, the FSC-generated prefix is omitted from **all** SQL table names.
This is useful when connecting to an existing database whose tables were created without
a prefix, or when the target database already enforces schema-level isolation and a
prefix would be redundant.

Default: `false` (prefix is applied as normal).

```yaml
token:
  storage:
    skipPrefix: true
```

With `skipPrefix: true` the table name pattern changes from `fsc_<short_code>_<params>`
to `<params>_<short_code>` (params still apply when provided; short-code overrides from
`tableNames` are also still respected).

> **Caution:** Enabling `skipPrefix` on a node that previously used the default prefix
> will cause the node to look for tables under different names. Make sure the underlying
> tables already exist under the unprefixed names before enabling this flag.

---

### Optional: token.tms.<name>.services.storage.cleanup

If not specified, the default configuration is:

```yaml
token:
  tms:
    <name>:
      services:
        storage:
          cleanup:
            enabled: false
            ttl: 24h
            scanInterval: 1h
            batchSize: 100
            workerCount: 1
            advisoryLockID: 8389190333894887277
            instanceID:
```

Default values:

- enabled: false
- ttl: 24h
- scanInterval: 1h
- batchSize: 100
- workerCount: 1
- advisoryLockID: 8389190333894887277 (`0x74746b636c65616e`)
- instanceID: empty, auto-generated when the cleanup manager starts

**Parameter Relationships and Tuning:**

- **Cleanup is disabled by default** and must be explicitly enabled. This is a conservative default to prevent unexpected key deletion in existing deployments.
- **Only deleted tokens older than `ttl` are considered for cleanup** to ensure tokens are truly finalized before key deletion.
- **The manager validates** that `ttl`, `scanInterval`, `batchSize`, and `workerCount` are all greater than zero.
- **`advisoryLockID`** is used to acquire PostgreSQL advisory-lock leadership so that only one replica performs a cleanup sweep at a time. The default value (8389190333894887277 or 0x74746b636c65616e) represents the ASCII string "ttkclean" (Token Transaction Keystore Cleanup) encoded as a 64-bit integer.
- **`instanceID`** is used to identify this replica in logs and monitoring; if omitted, the manager generates a unique identifier automatically at startup.

**Tuning Recommendations:**

1. **For High-Volume Environments:**
   - Increase `batchSize` to 200-500 to process more tokens per sweep
   - Increase `workerCount` to 8-16 to improve parallel key deletion
   - Decrease `scanInterval` to 30m for more frequent cleanup

2. **For Resource-Constrained Systems:**
   - Decrease `workerCount` to 2 to reduce CPU usage
   - Increase `scanInterval` to 2-4h to reduce database load
   - Keep default `batchSize` to limit memory usage

3. **For Security-Sensitive Deployments:**
   - Decrease `ttl` to 12h for faster key removal
   - Decrease `scanInterval` to 30m for more frequent cleanup
   - Monitor cleanup metrics to ensure timely processing

4. **For Multi-Instance Deployments:**
   - **PostgreSQL Required**: Multi-instance deployments require PostgreSQL for distributed coordination via advisory locks
   - Keep default `advisoryLockID` unless running multiple independent cleanup systems
   - Consider setting explicit `instanceID` values for easier debugging and monitoring

5. **For Single-Node Deployments:**
   - **SQLite Supported**: SQLite can be used for single-node deployments and handles node restarts gracefully
   - Cleanup works automatically after node restarts by scanning for eligible tokens
   - **Important**: Do not use SQLite with multiple replicas as it lacks the advisory lock mechanism for leader election

**Performance Considerations:**
- Each scan queries the token database, so `scanInterval` directly affects database load
- `workerCount` affects CPU utilization during cleanup sweeps
- `batchSize` affects memory usage and the duration of each cleanup sweep
- The relationship `scanInterval < ttl` ensures timely cleanup without premature processing

---
---

### Optional: token.tms.<name>.auditor.lock

If not specified, the default configuration is:

```yaml
token:
  tms:
    <name>:
      auditor:
        lock:
          maxRetries: 10
          initialBackoff: 10ms
          maxBackoff: 5s
          backoffMultiplier: 2.0
          jitterFactor: 0.3
```

Default values:

- maxRetries: 10
- initialBackoff: 10ms
- maxBackoff: 5s
- backoffMultiplier: 2.0
- jitterFactor: 0.3

**Parameter Descriptions:**

- **maxRetries**: Maximum number of retry attempts when acquiring locks on enrollment IDs during audit operations
- **initialBackoff**: Initial delay before the first retry attempt
- **maxBackoff**: Maximum delay between retry attempts (exponential backoff is capped at this value)
- **backoffMultiplier**: Factor by which the backoff delay increases after each retry (exponential growth)
- **jitterFactor**: Randomization factor (0.0 to 1.0) added to backoff delays to prevent multiple auditors from retrying simultaneously (prevents thundering herd problem)

**Tuning Recommendations:**

1. **For High-Contention Environments:**
   - Increase `maxRetries` to 15-20 to handle more lock conflicts
   - Increase `maxBackoff` to 10s to spread out retry attempts
   - Keep `jitterFactor` at 0.3 or higher to maintain randomization

2. **For Low-Latency Requirements:**
   - Decrease `initialBackoff` to 5ms for faster initial retries
   - Decrease `maxBackoff` to 2s to avoid long waits
   - Increase `backoffMultiplier` to 3.0 for faster exponential growth

3. **For Resource-Constrained Environments:**
   - Decrease `maxRetries` to 5 to fail faster
   - Keep default backoff settings to balance retry attempts with resource usage

2. **For Resource-Constrained Environments:**
   - Decrease `batchSize` to 50 to reduce memory usage
   - Decrease `workerCount` to 2 to reduce CPU load
   - Increase `scanInterval` to 10-15s to reduce database queries

3. **For Long-Running Transaction Assembly:**
   - Increase `ttl` to 60s or more to avoid premature recovery attempts
   - Ensure `leaseDuration` is at least 2x the expected transaction processing time

4. **For Multi-Instance Deployments:**
   - **PostgreSQL Required**: Multi-instance deployments require PostgreSQL for distributed coordination via advisory locks
   - Keep default `advisoryLockID` unless running multiple independent recovery systems
   - Consider setting explicit `instanceID` values for easier debugging and monitoring
   - Ensure all instances share the same PostgreSQL database for proper coordination

5. **For Single-Node Deployments:**
   - **SQLite Supported**: SQLite can be used for single-node deployments and handles node restarts gracefully
   - Recovery works automatically after node restarts by scanning for pending transactions
   - **Important**: Do not use SQLite with multiple replicas as it lacks the advisory lock mechanism for leader election

**Performance Impact:**
- Each scan queries the transaction database, so `scanInterval` directly affects database load
- `workerCount` affects CPU and network utilization during recovery sweeps
- `batchSize` affects memory usage and the duration of each recovery sweep
- The relationship `scanInterval < ttl` ensures timely detection without premature recovery

---

### Optional: token.tms.<name>.auditor.locker

Controls the distributed locking strategy used by the auditor to serialise
concurrent access to enrollment IDs (EIDs) when processing audit records.

If not specified, the default configuration is:

```yaml
token:
  tms:
    <name>:
      auditor:
        locker:
          backend: memory
          postgres:
            ttl: 30s
            acquireBackoff: 100ms
            acquireDeadline: 1m
            heartbeat: 10s
            owner:
```

Default values:

- backend: `memory` (in-process mutex, single-replica only)
- postgres.ttl: 30s
- postgres.acquireBackoff: 100ms
- postgres.acquireDeadline: 1m
- postgres.heartbeat: 10s
- postgres.owner: empty, defaults to the FSC node ID (`config.Provider.ID()`)

**Backend Selection:**

| Backend    | Use case                      | Database requirement |
|------------|-------------------------------|----------------------|
| `memory`   | Single-replica deployments    | Any (SQLite, Postgres) |
| `postgres` | Multi-replica deployments     | PostgreSQL only        |

**Notes:**
- The `postgres` backend uses a dedicated lease table (created automatically) with row-level locking, heartbeat renewal, and automatic expiry. It relies on PostgreSQL-specific SQL features (`ON CONFLICT DO UPDATE … RETURNING`, `::interval` casts, `TIMESTAMPTZ`).
- The `memory` backend uses in-process semaphores and provides no cross-replica coordination. It is suitable for single-node or development setups.
- When using `postgres`, all auditor replicas must share the same PostgreSQL database so that EID locks are globally visible.
- Set `heartbeat` to roughly `ttl / 3` to ensure leases are renewed well before expiry.
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
    # Built-in per-wallet rate limiter for token selection (both drivers).
    # It is disabled by default: without these keys, selection requests are not metered.
    # One selection request (a Selector.Select call) costs one unit, no matter how many tokens it
    # locks or how often it retries internally. Unlocking tokens is never throttled.
    # A throttled request fails fast with an error wrapping token.SelectorRateLimited.
    # See docs/security/selector_resource_limits.md.
    # rateLimitEnabled turns the limiter on with the default rate and burst below.
    rateLimitEnabled: true
    # rateLimit is the maximum number of selection requests per second a single wallet may issue.
    # A positive value implies rateLimitEnabled: true. If not specified or set to 0, defaults to 100.
    rateLimit: 100
    # rateLimitBurst is the maximum number of selection requests a single wallet may issue
    # back-to-back. If not specified or set to 0, defaults to twice rateLimit.
    rateLimitBurst: 200
    # rateLimitMaxBuckets caps the number of per-wallet buckets kept in memory. When the cap is
    # reached, idle buckets are pruned first and, if that is not enough, the least recently used
    # ones are dropped. If not specified or set to 0, defaults to 4096.
    rateLimitMaxBuckets: 4096

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
      # mapperParallelism is the number of goroutines that process incoming transactions in parallel.
      # A non-positive or unset value falls back to the default (10).
      mapperParallelism: 10
      # blockProcessParallelism is the number of blocks we can process in parallel when they arrive from the delivery service.
      # Set it to 1 (and only 1) to process blocks sequentially; this is the suggested
      # configuration if we are not sure about the dependencies between blocks.
      # A non-positive or unset value falls back to the default (10), NOT sequential mode.
      # The total go routines processing transactions will be blockProcessParallelism * mapperParallelism.
      blockProcessParallelism: 1
      # lruSize detects how many transactions we should guarantee to keep in our recent cache.
      # If the transaction is not among these elements, we proceed to step b, as described above.
      # Set it to 0 to disable the bound and never evict past transactions (the cache grows without limit).
      # An unset or negative value falls back to the default (30).
      lruSize: 30
      # eviction will happen when the cache size exceeds lruSize + lruBuffer.
      # Set it to 0 to disable the bound and never evict (same effect as lruSize: 0).
      # An unset or negative value falls back to the default (15).
      lruBuffer: 15
      # listenerTimeout is the duration to listen when we can't find a transaction in the cache (most probably it is about to become final).
      # We will listen for this amount of time and then we will query the whole ledger, as described in step c.
      # Set it to 0 to disable the timeout and wait forever for the transaction (as is done for the 'committer' type).
      # An unset or negative value falls back to the default (10s).
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
        
  # validation configures the resource limits enforced on untrusted token requests/actions
  # before they reach cryptographic verification. Process-wide (not per-TMS): both the FSC/DI
  # runtime and the standalone Fabric chaincode process resolve a single ResourceLimits value.
  # Every field is optional; any field left unset defaults to the safe, historical value.
  # The chaincode process (which has no config service) reads the same limits from
  # TOKEN_VALIDATION_MAX_* environment variables instead. See
  # docs/drivers/validation-resource-limits.md for the full enforcement and consensus-safety
  # contract: if you override any limit, every peer validating the same channel/namespace MUST
  # be configured with the identical value.
  validation:
    limits:
      maxRequestBytes: 262144
      maxActions: 256
      maxSignatures: 4096
      maxSignatureBytes: 4096
      maxActionBytes: 262144
      maxInputs: 256
      maxOutputs: 256
      maxMetadataEntries: 64
      maxMetadataKeyBytes: 256
      maxMetadataValueBytes: 4096
      maxProofBytes: 131072
      maxIdentityDepth: 5
      maxIdentityComponents: 16

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
              #     obtained via service discovery; on FabricX, via the query service. A policy requiring
              #     several signers from the same MSP is satisfied with that many *distinct* endorsers of
              #     that MSP, never the same endorser twice. Endorsement fails if the endorsers below
              #     cannot satisfy the namespace's policy.
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
              
              # batchSize is the maximum number of pending transactions claimed per scan. Default: 16.
              # Limits the number of transactions processed in a single recovery sweep to prevent
              # overwhelming the system. Increase for high-throughput environments with many pending transactions.
              # Performance impact: Larger batches reduce scan overhead but increase memory usage and processing time per sweep.
              batchSize: 16
              
              # workerCount is the number of local workers that process claimed transactions in parallel. Default: 8.
              # Increase to improve recovery throughput in high-volume scenarios.
              # Decrease to reduce resource consumption on constrained systems.
              # Performance impact: More workers increase CPU and network utilization but improve recovery speed.
              workerCount: 8
              
              # leaseDuration is how long a claimed transaction remains leased to this instance before it can be reclaimed. Default: 5m.
              # This prevents stuck transactions from blocking recovery indefinitely if a worker crashes.
              # Should be longer than the typical time to query and process a single transaction.
              # Relationship: Should be greater than the expected network latency + processing time for transaction status queries.
              leaseDuration: 5m

              # transactionTimeout bounds a single transaction's recovery attempt: the
              # handler's Recover call, which queries the ledger for status and applies
              # finality logic. Default: 60s. If set explicitly (and non-zero), rejected
              # below a 10s floor: a shorter deadline risks abandoning recoveries that were
              # merely slow, not genuinely stuck.
              # Without this, a hung status query blocks the worker indefinitely, and this
              # instance holds recovery leadership for as long as anything is abandoned in
              # the background (see the leadership note further down), so a permanently
              # hung status query stalls recovery on every replica.
              # Relationship: workerCount workers can each be stuck for the full
              # transactionTimeout at once, so size leaseDuration against
              # leaseDuration > (batchSize / workerCount) x transactionTimeout. Also keep
              # scanInterval comfortably below leaseDuration: a stuck transaction's claim
              # is kept alive only by this instance's own next sweep reclaiming it, so a
              # scanInterval close to or above leaseDuration risks the claim lapsing
              # between sweeps. The manager Warns at startup (does not fail to start) when
              # either does not hold: an attempt still in flight when this happens keeps
              # its claim rather than releasing it, so this is a throughput concern, not a
              # correctness one.
              # Set to 0 to disable the deadline entirely. Do this only if you accept the
              # tradeoff: a Recover call that ignores its context can then block Stop()
              # indefinitely, since a deadline is the only thing that can interrupt a call
              # the callee itself never reads.
              transactionTimeout: 60s

              # stuckTransactionAlertThreshold escalates the per-attempt failure log from
              # Warn to Error once the same transaction has hit transactionTimeout this many
              # consecutive times, so a persistently-unresponsive peer is visible to alerting
              # instead of blending into routine sweep failures. Default: 5.
              # Log-severity only: it never changes the transaction's status, and it never
              # changes when the manager skips a re-attempt (that happens independently,
              # whenever an earlier attempt for the same tx hasn't returned yet). Past the
              # threshold, the log backs off by doubling rather than firing every sweep.
              # Set to 0 to disable the escalation.
              stuckTransactionAlertThreshold: 5
              
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
          # memory section is read only when backend == "memory".
          memory:
            # acquireDeadline bounds how long one acquisition waits for an EID held
            # by another anchor. Every backend bounds its own waiting so that a
            # caller which passes no deadline still gets an answer, and so that
            # spending the whole budget can be reported as such; auditor.lock does
            # not retry an acquisition that already exhausted it. Defaults to the
            # same 1m as the postgres backend, so switching backends does not
            # silently change how long an audit can block.
            acquireDeadline: 1m
          # postgres section is read only when backend == "postgres".
          postgres:
            # ttl is the lease duration for each EID lock row.
            ttl: 30s
            # acquireBackoff is the initial wait between retry attempts when a lock
            # is contended. Successive waits grow exponentially and are jittered,
            # so this is the floor rather than a fixed poll interval.
            acquireBackoff: 100ms
            # acquireMaxBackoff caps that growth. Raised to acquireBackoff if set
            # below it.
            acquireMaxBackoff: 2s
            # acquireDeadline is the total time allowed to acquire all EID locks,
            # and the whole budget for waiting out contention: auditor.lock does
            # not retry an acquisition that already exhausted it.
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

        # auditTokensRetry controls how the auditor's early validation gate
        # (AuditorCheck) tolerates the read-timing race in which a referenced
        # token's producing transaction is still pending, so its outputs have not
        # yet been persisted to the token store by the asynchronous finality
        # listener. When a lookup misses and the producing tx is still pending,
        # the gate waits and retries instead of spuriously rejecting the request.
        auditTokensRetry:
          # numRetries is the number of token-lookup attempts before giving up.
          # There are numRetries-1 delays between attempts. Values <= 0 (or an
          # omitted key) keep the default; a single attempt is always made.
          # Default: 3
          numRetries: 3
          # retryDelay is the backoff slept between attempts, as a duration string
          # (e.g. 500ms, 3s). Must be > 0; an invalid or non-positive value falls
          # back to the default. The backoff honors context cancellation.
          # Default: 3s
          retryDelay: 3s
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
- rateLimitEnabled: false (the built-in per-wallet selection rate limiter is opt-in)
- rateLimit: 100 requests/s per wallet, when rate limiting is enabled
- rateLimitBurst: 2 × rateLimit, so 200 requests, when rate limiting is enabled
- rateLimitMaxBuckets: 4096

Setting a positive `rateLimit` is enough to enable the limiter; `rateLimitEnabled: true` alone
enables it with the defaults above. See
[docs/security/selector_resource_limits.md](security/selector_resource_limits.md) for what is
metered and how to plug in your own limiter instead.

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

### Optional: token.validation.limits

Process-wide (not per-TMS) resource limits enforced on untrusted token requests/actions before
cryptographic verification. See [docs/drivers/validation-resource-limits.md](drivers/validation-resource-limits.md)
for the full enforcement points and the consensus-safety contract.

If not specified, the default configuration is:

```yaml
token:
  validation:
    limits:
      maxRequestBytes: 262144
      maxActions: 256
      maxSignatures: 4096
      maxSignatureBytes: 4096
      maxActionBytes: 262144
      maxInputs: 256
      maxOutputs: 256
      maxMetadataEntries: 64
      maxMetadataKeyBytes: 256
      maxMetadataValueBytes: 4096
      maxProofBytes: 131072
      maxIdentityDepth: 5
      maxIdentityComponents: 16
```

Default values:

- maxRequestBytes: 262144 (256 KiB)
- maxActions: 256
- maxSignatures: 4096
- maxSignatureBytes: 4096 (4 KiB)
- maxActionBytes: 262144 (256 KiB)
- maxInputs: 256
- maxOutputs: 256
- maxMetadataEntries: 64
- maxMetadataKeyBytes: 256
- maxMetadataValueBytes: 4096 (4 KiB)
- maxProofBytes: 131072 (128 KiB) — ignored by drivers without a zero-knowledge proof (fabtoken)
- maxIdentityDepth: 5 — how deeply composite owner identities (multisig, policy, HTLC script) may
  nest inside one another. Real deployments nest 2–3 levels, e.g. a policy over a multisig over x509
- maxIdentityComponents: 16 — how many component identities a single composite identity may carry.
  Bounds fan-out, which maxIdentityDepth does not

Every field is optional; any field omitted (or the whole `token.validation.limits` key omitted)
resolves to its default. Read via the config service, so this key applies only to the FSC/DI
runtime. The standalone Fabric chaincode process (`token/services/network/fabric/tcc/main`) has no
config service and instead reads the equivalent `TOKEN_VALIDATION_MAX_*` environment variables
(e.g. `TOKEN_VALIDATION_MAX_ACTIONS`), with the same unset-defaults overlay.

**Consensus-safety warning:** the defaults above are safe and identical across every peer that
does not override them. If you override any limit, every peer validating the same
channel/namespace — and the chaincode process, if it enforces limits independently — MUST be
configured with the identical value, or endorsement determinism silently breaks (one peer accepts
a request another rejects). Treat a limits change like a `driver.MaxAnchorSize` change: roll it
out as a coordinated configuration change before any peer relies on the new value.

---

### Optional: token chaincode query limits (environment only)

The read-only query functions of the token chaincode (`queryStates`, `queryTokens`,
`areTokensSpent`) perform one ledger read per element of the caller-supplied array, so they are
bounded independently of `token.validation.limits`. These limits live in the chaincode process, not
in the FSC node's configuration file, and are read from the environment:

| Environment variable | Default | Bounds |
| --- | --- | --- |
| `TOKEN_QUERY_MAX_REQUEST_BYTES` | 1048576 (1 MiB) | Raw size of the query argument, checked before it is decoded |
| `TOKEN_QUERY_MAX_ITEMS` | 4096 | Number of elements, checked before the first ledger read |

Both are optional; an unset variable resolves to its default, and an unparseable value is a startup
error. Unlike `token.validation.limits` these values are **not** consensus-relevant — the query path
performs no writes and is not an endorsement boundary — so they do not need to match across peers.
See [Token Chaincode Query Limits](security/tcc_query_limits.md).

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
              batchSize: 16
              workerCount: 8
              leaseDuration: 5m
              transactionTimeout: 60s
              instanceID:
              notFoundGracePeriod: 30m
              stuckTransactionAlertThreshold: 5
```

Default values:

- enabled: true
- ttl: 30s
- scanInterval: 5s
- batchSize: 16
- workerCount: 8
- leaseDuration: 5m
- transactionTimeout: 60s (set to 0 to disable the per-transaction deadline; an explicit non-zero value below 10s is rejected)
- instanceID: empty, auto-generated when the recovery manager starts
- notFoundGracePeriod: 30m (set to 0 to disable promotion to Orphan)
- stuckTransactionAlertThreshold: 5 (set to 0 to disable the log escalation)

**Parameter Relationships and Tuning:**

- **Recovery is enabled by default** to ensure automatic recovery of lost finality listeners.
- **Only pending transactions older than `ttl` are considered for recovery** to avoid interfering with active transaction processing.
- **The manager validates** that `ttl`, `scanInterval`, `batchSize`, and `workerCount` are all greater than zero, that `leaseDuration` is greater than zero, and that `transactionTimeout` is not negative (`0` means unbounded; any positive value is accepted without an upper bound, though loading the configuration separately rejects an explicit non-zero value below a 10s floor, see the next bullet). It additionally **Warns at startup, without failing to start,** when `leaseDuration > (batchSize / workerCount) x transactionTimeout` or `scanInterval < leaseDuration` do not hold, see the next bullet.
- **Recovery leadership** uses a PostgreSQL advisory lock so that only one replica performs a recovery sweep at a time, on the owner transaction path. The lock identifier is no longer configurable: it is derived from the TMS's own `requests` table name, which already carries the network, channel and namespace. That makes it unique per TMS automatically, so several TMSes sharing one PostgreSQL database no longer compete for a single lock, and there is nothing left for an operator to keep unique by hand. Leadership is normally released at the end of each sweep, but held across sweeps for as long as any transaction is still abandoned in the background from a previous sweep (see `transactionTimeout` below), so this instance keeps winning it — and thereby keeps its own reclaim renewing that transaction's lease — instead of leaving that to chance; `Stop()` always releases it regardless. The audit transaction store does not yet participate in this coordination — its recovery leader factory is nil, so every replica is granted leadership and the audit sweep runs redundantly on each of them; see [#2143](https://github.com/LFDT-Panurus/panurus/issues/2143).
- **`transactionTimeout`** bounds a single `Handler.Recover()` call, not the whole sweep. The call runs in its own goroutine so the worker can give up on the deadline even if the call itself never honours the context. A per-transaction bound does not bound the sweep: a batch of `batchSize` claims split across `workerCount` workers can take up to `(batchSize / workerCount) × transactionTimeout` worst case, so the invariant that actually matters is `leaseDuration > (batchSize / workerCount) × transactionTimeout`. With the shipped defaults that worst case is `2 × 60s = 120s`, comfortably inside the default 5m `leaseDuration`. Separately, `scanInterval` should stay comfortably below `leaseDuration`: a stuck transaction's claim is kept alive only by this instance's own next sweep reclaiming the still-`Pending` row (which refreshes the claim as a side effect), and this instance holds recovery leadership across sweeps for as long as any transaction stays abandoned specifically so that reclaim keeps happening on this instance rather than depending on it winning leadership again by chance, but a `scanInterval` close to or above `leaseDuration` still risks that renewal arriving too late between this instance's own sweeps. If you only set `leaseDuration` and leave `transactionTimeout` unset, the manager automatically lowers the 60s default below whatever `leaseDuration` you configured (floored at 10s) rather than leaving it at a disproportionate default. An explicit, non-zero `transactionTimeout` below 10s is rejected when the configuration is loaded: a deadline that tight is likely to abandon recoveries that were merely slow on a loaded network, not genuinely stuck. `transactionTimeout: 0` disables the deadline entirely, but `Stop()` does **not** bound a ctx-blind call the way the deadline does; disabling it can leave `Stop()` blocked indefinitely on a hung `Recover` call (the manager logs a Warn at startup when this is set). Note the bound stops at `Handler.Recover()`: `ClaimPendingTransactions`, `ReleaseRecoveryClaim`, and `SetStatus` still run on the sweep's own context, which carries no deadline, so a wedged database connection in any of those still blocks the worker and, since leadership is held for as long as anything is abandoned, every replica of the TMS in the meantime, the same stall `transactionTimeout` was added to fix for the ledger side. `transactionTimeout` is a bound on the handler call only, not a general no-stall guarantee for the sweep. A `recoverCtx.Done()` firing because `Stop()` cancelled the manager, rather than because `transactionTimeout` actually elapsed, is not counted or alerted on as a timeout.
- **`instanceID`** is used as the lease owner identifier for claimed transactions; if omitted, the manager generates a unique UUID automatically at startup.
- **`notFoundGracePeriod`** controls how long a transaction whose status query keeps returning `NotFound` is left in `Pending` before being promoted to the terminal `Orphan` status. This protects the recovery sweep from being permanently blocked by transactions whose audit log was persisted but whose broadcast never reached the ledger. The promoted row is marked `Orphan` rather than `Deleted` so operators can distinguish broadcast failures from ledger-rejected transactions. Raise the default if your network has long catch-up windows after committer/orderer restarts; set it to `0` to disable the promotion entirely.
- **`stuckTransactionAlertThreshold`** escalates the per-attempt failure log from Warn to Error once the same transaction has hit `transactionTimeout` this many consecutive times, so a persistently-unresponsive peer surfaces to alerting instead of blending into routine sweep-failure logs. Past the threshold, the log backs off by doubling (at the threshold, then `2×`, `4×`, `8×`, ...) rather than firing on every sweep, so a transaction stuck for hours does not flood alerting. It is log-severity only: a timeout means the status query didn't answer in time, not that the transaction failed. Unlike `notFoundGracePeriod`, it never changes the transaction's status. The row is still reclaimed every sweep, but while an earlier attempt is still running past its own `transactionTimeout`, the manager skips starting a second one for the same transaction, without releasing its claim, rather than running duplicate concurrent `Recover` calls; the claim is released once that earlier attempt actually returns, whenever that is. Set to `0` to disable the escalation.

**Tuning Recommendations:**

1. **For High-Throughput Environments:**
   - Increase `batchSize` to 100-500 to process more transactions per sweep
   - Increase `workerCount` to 16-32 to improve parallel processing
   - Decrease `scanInterval` to 2-3s for faster recovery detection
   - Raise `leaseDuration` alongside `batchSize`/`workerCount` to keep `leaseDuration > (batchSize / workerCount) x transactionTimeout`


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

> **Note:** `<params>` is the TMS identity (network, channel, namespace). `_`, `-` and `.`
> in those values are escaped to `__`, `_d` and `_f`; letters, digits and underscores pass
> through unchanged, so a channel named `channel1` is fine. The composed name must still be
> a valid SQL identifier — it cannot start with a digit, which is only reachable with
> `skipPrefix: true` and a network name starting with a digit. Any other character is a
> configuration error and is reported as such, not a crash. See
> [Table Name Customisation](services/storage.md#table-name-customisation).

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
            instanceID:
```

Default values:

- enabled: false
- ttl: 24h
- scanInterval: 1h
- batchSize: 100
- workerCount: 1
- instanceID: empty, auto-generated when the cleanup manager starts

**Parameter Relationships and Tuning:**

- **Cleanup is disabled by default** and must be explicitly enabled. This is a conservative default to prevent unexpected key deletion in existing deployments.
- **Only deleted tokens older than `ttl` are considered for cleanup** to ensure tokens are truly finalized before key deletion.
- **The manager validates** that `ttl`, `scanInterval`, `batchSize`, and `workerCount` are all greater than zero.
- **Cleanup leadership** uses a PostgreSQL advisory lock so only one replica sweeps at a time. The identifier is not configurable: it is derived from the TMS's own token SKI cleanup table name, which already carries network, channel and namespace, so it is unique per TMS automatically.
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

**Relationship to `auditor.locker`:** this retry covers failures that another attempt
might survive, such as a transient database error. It deliberately does **not** retry
an acquisition that failed with `ErrLockAcquireTimeout`, because that means the locker
already spent its own `acquireDeadline` waiting out the contention — retrying would
spend it again. Waiting under contention is configured once, in
[`auditor.locker`](#optional-tokentmsauditorlocker), so `maxRetries` here does not
multiply `acquireDeadline` there.

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
   - Decrease `batchSize` to 8 to reduce memory usage
   - Decrease `workerCount` to 2 to reduce CPU load
   - Increase `scanInterval` to 10-15s to reduce database queries

3. **For Long-Running Transaction Assembly:**
   - Increase `ttl` to 60s or more to avoid premature recovery attempts
   - Ensure `leaseDuration` is at least 2x the expected transaction processing time

4. **For Multi-Instance Deployments:**
   - **PostgreSQL Required**: Multi-instance deployments require PostgreSQL for distributed coordination via advisory locks
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

### Optional: token.tms.<name>.auditor.auditTokensRetry

Controls how the auditor's early validation gate (`AuditorCheck`) tolerates the
read-timing race in which a referenced token's producing transaction is still
pending, so its outputs have not yet been persisted to the token store by the
asynchronous finality listener. On a missing lookup whose producing transaction is
still `Pending`, the gate waits `retryDelay` and retries up to `numRetries`
attempts before failing, instead of spuriously rejecting a validly-audited,
quickly-chained transaction. This mirrors the tolerance already applied on the
sibling `Audit()` path. Applies to both the `fabtoken` and `dlog` drivers.

If not specified, the default configuration is:

```yaml
token:
  tms:
    <name>:
      auditor:
        auditTokensRetry:
          numRetries: 3
          retryDelay: 3s
```

Default values:

- numRetries: 3
- retryDelay: 3s

**Parameter Descriptions:**

- **numRetries**: Number of token-lookup attempts made before giving up, with
  `numRetries - 1` backoff delays between them (so the defaults give a ~6s grace
  window: 3 attempts, 2 delays of 3s). A value `<= 0`, or an omitted key, keeps the
  default; at least one attempt is always made. Retries are only spent while a
  referenced transaction is genuinely `Pending` — a hard lookup failure (or a
  failure to determine pending status) fails fast without consuming the budget.
- **retryDelay**: Backoff slept between attempts, expressed as a Go duration string
  (e.g. `500ms`, `3s`). Must be `> 0`; an invalid or non-positive value logs a
  warning and falls back to the default. The backoff honors context cancellation,
  so a cancelled or timed-out request returns immediately rather than pinning a
  goroutine for the full grace window.

**Tuning Recommendations:**

- **For quickly-chained, high-throughput workloads** (an output spent shortly after
  it was audited), raise `numRetries` and/or `retryDelay` to widen the grace window
  and further reduce spurious rejections when finality persistence lags.
- **For latency-sensitive gates**, lower `retryDelay` (e.g. `500ms`) so the audit
  gate fails faster when the producing transaction never becomes final, at the cost
  of tolerating a shorter persistence lag.

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
            acquireMaxBackoff: 2s
            acquireDeadline: 1m
            heartbeat: 10s
            owner:
```

Default values:

- backend: `memory` (in-process mutex, single-replica only)
- postgres.ttl: 30s
- postgres.acquireBackoff: 100ms (initial wait; grows exponentially, jittered)
- postgres.acquireMaxBackoff: 2s (cap on that growth; raised to `acquireBackoff` if set below it)
- postgres.acquireDeadline: 1m (the whole budget for waiting out contention)
- postgres.heartbeat: 10s
- postgres.owner: empty, defaults to the FSC node ID (`config.Provider.ID()`). Required
  when `backend: postgres` — if both this value and `fsc.id` are empty or blank, the
  locker fails to start with `auditor locker owner is required` (see the note below).

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
- `owner` identifies the replica holding each lease and **must be non-empty and unique per replica**: every lease query (acquire, release, renew, and the pre-write `AssertLocksHeld` check) is scoped by it. If several replicas shared one owner value, those predicates would match each other's rows and mutual exclusion would be lost cluster-wide, so an empty or blank resolved owner is rejected at startup rather than tolerated. A typical cause is a templated node configuration with `fsc.id` left unset. No owner is synthesized as a fallback, because an owner that changed on each restart would leave a replica unable to renew or release the leases it still holds. The `memory` backend has no owner and is unaffected.

---

### Optional: token.tms.<name>.identity.throttle

Escalating throttle policy on the signer/verifier surface: per-principal rate limiting plus
automatic escalation on error and invalid-signature ratios. See
[docs/security/signature_observability.md](security/signature_observability.md) for the metrics,
the audit trail, and the enforcement boundary.

If not specified, the default configuration is:

```yaml
token:
  tms:
    <name>:
      identity:
        throttle:
          mode: monitor
          rate: 200
          burst: 400
          window: 1m
          minSamples: 50
          errorRateThreshold: 0.5
          invalidSignatureRateThreshold: 0.2
          quotaReductionFactor: 0.25
          softDuration: 5m
          blockDuration: 1m
          deescalateAfter: 5m
          idleTTL: 10m
```

Default values:

- mode: monitor
- rate: 200
- burst: 400
- window: 1m
- minSamples: 50
- errorRateThreshold: 0.5
- invalidSignatureRateThreshold: 0.2
- quotaReductionFactor: 0.25
- softDuration: 5m
- blockDuration: 1m
- deescalateAfter: 5m
- idleTTL: 10m

**Parameter Descriptions:**

- **mode**: `off` (nothing metered, nothing denied), `monitor` (evaluate and report, never deny) or `enforce` (deny throttled principals)
- **rate**: Metered signature operations per second allowed per principal; a negative value disables the policy like `off`
- **burst**: Token bucket capacity, absorbing short spikes without raising the sustained rate; values below `rate` are raised to `rate`
- **window**: Period over which the error and invalid-signature ratios are evaluated
- **minSamples**: Minimum number of observations in a window before a ratio can escalate a principal
- **errorRateThreshold**: Fraction of failing operations in a window that escalates a principal; a value greater than `1` disables this trigger
- **invalidSignatureRateThreshold**: Fraction of rejected verifications in a window that escalates a principal
- **quotaReductionFactor**: Multiplier applied to `rate` and `burst` while a principal is soft-limited; must be in `(0,1]`
- **softDuration**: Minimum time a principal stays on the reduced quota
- **blockDuration**: How long a blocked principal is refused before being released back to the reduced quota
- **deescalateAfter**: Violation-free period required before a level is restored
- **idleTTL**: How long per-principal state is kept after its last operation

**Notes:**

- The default `monitor` mode reports what `enforce` would have denied, so thresholds can be tuned
  against production traffic before they bite.
- Out-of-range values (an `invalidSignatureRateThreshold` below 0, a `quotaReductionFactor` outside
  `(0,1]`, an unknown `mode`) fail at startup rather than being clamped.
- Denied operations return an error wrapping `token.SignatureThrottled`; callers detect it with
  `errors.Is` and back off.

# Keystore Cleanup Service

The **Keystore Cleanup Service** provides automatic deletion of cryptographic keys from the keystore for tokens that have been deleted (spent, expired, or invalidated). This ensures that the keystore doesn't accumulate stale keys indefinitely, improving security and reducing storage overhead.

## Overview

The cleanup system consists of four main components:

1. **Manager**: Orchestrates the cleanup process with periodic scanning and distributed coordination
2. **SKI Extraction System**: Pluggable architecture for deriving Subject Key Identifiers (SKIs) from owner identities
3. **Storage Interface**: Provides database operations for querying deleted tokens and tracking cleanup state
4. **Keystore Interface**: Abstracts key deletion operations

## Architecture

### Cleanup Manager

The Manager runs in the background and periodically scans for deleted tokens that are eligible for key cleanup. It uses distributed locking (PostgreSQL advisory locks) to ensure only one replica in a multi-instance deployment performs cleanup at a time.

**Key Features:**
- Periodic scanning with configurable intervals
- Worker pool for parallel key deletion (created per sweep)
- Distributed leadership via advisory locks
- Idempotent operations with cleanup tracking
- Immediate initial sweep on startup

**Lifecycle:**
- Leadership is acquired per sweep, not held continuously
- Initial sweep runs immediately on start (before first interval)
- Worker pool is created and destroyed for each sweep
- Context cancellation stops workers gracefully mid-sweep

### SKI Extraction Architecture

The SKI (Subject Key Identifier) extraction system uses a pluggable provider architecture to support different identity types. This allows identity-type-specific logic to be encapsulated in separate providers.

**Components:**

1. **TypedSKIProvider Interface**: Defines the contract for identity-type-specific SKI extraction
   ```go
   type TypedSKIProvider interface {
       // GetSKIsFromIdentity derives one or more SKIs from an identity's raw bytes
       GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error)
   }
   ```

2. **SKIExtractor**: Orchestrates SKI extraction by maintaining a registry of providers
   - Maps identity types (e.g., "idemix", "x509") to their specific providers
   - Routes extraction requests to the appropriate provider
   - Falls back to default provider for unknown types
   - Thread-safe for concurrent use after initialization

3. **Built-in Providers**:
   - **IdemixSKIProvider**: Extracts SKI from Idemix NymPublicKey
   - **IdemixNymSKIProvider**: Extracts SKI from Idemix pseudonym identities
   - **NoopSKIProvider**: Derives no SKIs, marking an identity type as deliberately excluded from
     cleanup. Registered for X.509 — see [X.509 is intentionally out of scope](#x509-is-intentionally-out-of-scope)
   - **FallbackSKIProvider**: Computes SHA256 hash of identity bytes as SKI (default)

**Provider Registration:**
```go
extractor := NewSKIExtractor()
extractor.RegisterProvider("idemix", idemix.NewSKIProvider())
extractor.RegisterProvider("idemixnym", idemixnym.NewSKIProvider(identityStore))
// X.509 keys belong to the wallet, not to individual tokens: deliberately never cleaned up
extractor.RegisterProvider("x509", NewNoopSKIProvider())
// Fallback provider is used for any unregistered types
```

**SKI Derivation Process:**
1. SKIExtractor receives identity bytes and type
2. Looks up registered provider for that type
3. If found, delegates to type-specific provider
4. If not found, uses fallback provider (SHA256 hash)
5. Returns list of SKI strings in hexadecimal format

### X.509 is intentionally out of scope

X.509 is registered with `NoopSKIProvider`, so **no keystore key is ever deleted for an
X.509-owned token**. This is deliberate, not an unimplemented provider.

An X.509 owner identity is a long-lived, non-anonymous certificate: `x509.KeyManager` reports
`Anonymous() == false` and always serves the same identity descriptor. Its private key therefore
belongs to the **wallet**, not to any individual token — the same key signs every token that
wallet ever owns, and it remains in use long after those tokens are spent. A real X.509 SKI
provider would make the cleanup sweep delete the wallet's own signing key as soon as the first of
its tokens aged past the TTL, permanently breaking the wallet.

The Idemix providers are the opposite case: their SKIs identify a one-shot pseudonym key created
for a single recipient identity. That key is dead once its token is deleted, which is exactly what
makes it safe to remove.

**Consequence to be aware of:** a deleted X.509-owned token still gets a row in
`token_ski_cleanups`, because the cleanup manager records "no key material to delete" the same way
it records a completed deletion. For X.509 that row means *nothing to clean*, not *keys were
removed*. Do not read the `token_ski_cleanups` table as evidence that X.509 key material was
purged.

### Interfaces

#### Storage Interface

The Storage interface abstracts database operations needed for cleanup:

```go
type Storage interface {
    // AcquireCleanupLeadership obtains distributed lock for leader election
    AcquireCleanupLeadership(ctx context.Context) (Leadership, bool, error)
    
    // GetDeletedTokensPendingSKICleanup queries deleted, owned tokens older than TTL that haven't been cleaned.
    // Tokens for which this node is only an auditor or issuer are excluded, since this node
    // never holds the secret keys for those tokens.
    GetDeletedTokensPendingSKICleanup(ctx context.Context, olderThan time.Duration, limit int) ([]DeletedToken, error)
    
    // MarkTokenCleaned records successful key cleanup to prevent reprocessing
    MarkTokenCleaned(ctx context.Context, txID string, index uint64, cleanedBy string) error
}

type Leadership interface {
    // Close releases the leadership lock
    Close() error
}

type DeletedToken struct {
    TxID          string
    Index         uint64
    OwnerIdentity []byte
    OwnerType     string
    DeletedAt     time.Time
}
```

#### SKI Provider Interface

```go
type SKIProvider interface {
    // GetSKIsFromIdentity derives one or more SKIs from an owner identity
    GetSKIsFromIdentity(ctx context.Context, identity []byte, identityType string) ([]string, error)
}
```

#### Keystore Interfaces

```go
type Keystore interface {
    // Delete removes the key with the given identifier
    Delete(id string) error
    // Close closes the keystore
    Close() error
}

type KeystoreProvider interface {
    // Keystore returns the keystore for the given TMS
    Keystore(tmsID token.TMSID) (Keystore, error)
}
```

## Configuration

Cleanup behavior is controlled via configuration (see [Configuration](../../configuration.md)):

```yaml
services:
  storage:
    cleanup:
      enabled: false             # Disabled by default - must be explicitly enabled
      ttl: 24h                   # Minimum age before cleanup
      scanInterval: 1h           # How often to scan
      batchSize: 100            # Max tokens per scan
      workerCount: 1            # Parallel workers (default: 1)
      instanceID: "cleanup-1"   # Instance identifier (auto-generated if empty)
```

**Configuration Details:**

- **enabled**: Must be explicitly set to `true` to activate cleanup (conservative default)
- **ttl**: Minimum age of deleted tokens before their keys are eligible for cleanup
- **scanInterval**: How frequently the manager scans for eligible tokens
- **batchSize**: Maximum number of tokens processed in a single sweep
- **workerCount**: Number of parallel workers processing tokens within a sweep (default: 1)
- **instanceID**: Identifies the cleanup instance; auto-generated as `cleanup-<pointer>` if not provided

Leader election uses a PostgreSQL advisory lock whose identifier is not configurable. It is derived
from the TMS's own token SKI cleanup table name, which already carries network, channel and
namespace, so it is unique per TMS automatically and several TMSes sharing one database no longer
compete for a single lock.

**Configuration Loading:**

The configuration is loaded from the TMS configuration using the key `services.storage.cleanup`. The `LoadConfig()` function merges provided values with defaults, preserving defaults for any unset values.

## Usage

### Creating a Cleanup Manager

```go
import (
    "github.com/LFDT-Panurus/panurus/token"
    "github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
)

// Create configuration
config := cleanup.Config{
    Enabled:         true,
    TTL:             24 * time.Hour,
    ScanInterval:    1 * time.Hour,
    BatchSize:       100,
    WorkerCount:     4,
    InstanceID:      "cleanup-instance-1",
}

// Create TMS ID
tmsID := token.TMSID{
    Network:   "fabric",
    Channel:   "mychannel",
    Namespace: "token-chaincode",
}

// Create manager
manager := cleanup.NewManager(
    logger,              // logging.Logger
    storage,             // Storage interface implementation
    skiProvider,         // SKIProvider interface implementation
    keystoreProvider,    // KeystoreProvider interface implementation
    tmsID,               // Token Management System ID
    config,              // Configuration
)

// Start cleanup (runs initial sweep immediately)
if err := manager.Start(); err != nil {
    return err
}

// Stop cleanup gracefully
defer manager.Stop()
```

### Registering Custom SKI Providers

To add support for a custom identity type:

```go
// Implement TypedSKIProvider interface
type CustomSKIProvider struct {
    // your fields
}

func (p *CustomSKIProvider) GetSKIsFromIdentity(ctx context.Context, identity []byte) ([]string, error) {
    // Parse identity and extract SKIs
    // Return SKIs as hexadecimal strings
    return []string{"ski1", "ski2"}, nil
}

// Register with extractor
extractor := cleanup.NewSKIExtractor()
extractor.RegisterProvider("custom-type", &CustomSKIProvider{})
```

### Integration with TMS

The cleanup service is typically integrated automatically through the service manager:

```go
import (
    "github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
)

// Create service manager (manages cleanup instances per TMS)
cleanupManager := cleanup.NewServiceManager(
    configuration,           // Configuration interface
    identityStorageProvider, // Identity storage provider
    tokensProvider,          // Tokens service manager
)

// Service manager automatically:
// - Creates cleanup manager per TMS
// - Registers built-in SKI providers (idemix, idemixnym, x509)
// - Sets up storage and keystore adapters
// - Starts the cleanup manager
```

## Cleanup Process Flow

1. **Startup**: Manager starts and runs initial sweep immediately
2. **Leadership Acquisition**: Manager attempts to acquire PostgreSQL advisory lock
3. **Query Eligible Tokens**: If leadership acquired, query deleted tokens older than TTL that haven't been cleaned yet
4. **Create Worker Pool**: Spawn configured number of workers for this sweep
5. **Distribute Work**: Fan out tokens to worker pool via channel
6. **Per Token Processing**:
   - Get keystore for TMS
   - Derive SKIs from owner identity using appropriate provider
   - Delete each SKI from keystore
   - Mark token as cleaned in database **only if every key was deleted** (or if the owner type has
     no keys to delete at all)
7. **Release Leadership**: Close leadership lock
8. **Wait**: Sleep until next scan interval
9. **Repeat**: Go to step 2

**Timing Details:**
- Initial sweep runs immediately on `Start()`, before first interval
- Leadership is acquired and released for each sweep
- Worker pool is created per sweep, not persistent
- Context cancellation stops workers gracefully

## Error Handling

The cleanup service handles errors gracefully with specific retry behavior:

### Key Deletion Errors

Key deletion is **all-or-nothing** per token:

- **Any key fails to delete**: Token is NOT marked as cleaned; the whole token is retried on the
  next sweep. The returned error joins every per-key cause, so callers can inspect them with
  `errors.Is`/`errors.As`
- **All keys deleted**: Token IS marked as cleaned
- **No SKIs derived**: Token IS marked as cleaned to avoid infinite retries; logs warning. This is
  the normal path for X.509 — see
  [X.509 is intentionally out of scope](#x509-is-intentionally-out-of-scope)

### Rationale

Marking a token cleaned while some of its key material is still in the keystore would turn a
transient database error into a permanent, un-retriable key-retention hole: the token would never
be selected again, so the surviving key would never be deleted. Retrying the whole token is safe
and cheap because `Keystore.Delete` is idempotent — re-deleting the keys that already succeeded
costs one no-op call each.

Empty SKI cases are still marked complete, otherwise every sweep would rescan the same tokens
forever.

### Other Errors

- **Key Not Found**: Logged as warning, continues with other keys
- **Database Errors**: Logged and retried on next scan
- **Leadership Loss**: Cleanup stops gracefully, another instance takes over
- **Context Cancellation**: Workers stop gracefully mid-sweep

## Token Lifecycle States

A token transitions through the following states related to cleanup:

- **Active**: Token is unspent and in use
- **Deleted**: Token marked as deleted (`is_deleted=true`, `spent_at` set)
- **Eligible for Cleanup**: Deleted, **owned** token (`owner=true`) older than TTL without a cleanup record in `token_ski_cleanups`
- **Cleaned**: Keys deleted from keystore (record exists in `token_ski_cleanups`)

The cleanup service only processes tokens in the "Eligible for Cleanup" state. Tokens for which
this node is only an auditor or issuer (`owner=false`) are never selected, since this node
never holds the secret keys for tokens it does not own.

## Database Schema

The cleanup service uses a dedicated tracking table to record cleanup operations:

**Token SKI Cleanups Table:**
```sql
CREATE TABLE IF NOT EXISTS token_ski_cleanups (
    tx_id TEXT NOT NULL,
    idx INT NOT NULL,
    cleaned_at TIMESTAMP NOT NULL,
    cleaned_by TEXT NOT NULL,
    PRIMARY KEY (tx_id, idx),
    FOREIGN KEY (tx_id, idx) REFERENCES tokens
);
CREATE INDEX IF NOT EXISTS idx_cleaned_at_token_ski_cleanups ON token_ski_cleanups ( cleaned_at );
```

This table tracks when each token's cryptographic keys were cleaned from the keystore, preventing reprocessing. The `cleaned_by` field records which cleanup instance performed the operation, useful for debugging in multi-instance deployments.

The `token_ski_cleanups` table is automatically created by the schema initialization and does not require manual database alterations.

## Distributed Deployment

### PostgreSQL
- **Multi-Instance Support**: Uses advisory locks for distributed coordination
- **Leader Election**: Only one replica performs cleanup sweeps at a time
- **High Availability**: Multiple replicas can share the same database
- **Automatic Failover**: If leader fails, another replica acquires leadership on next scan
- **Lock Scope**: Leadership is per-sweep, not held continuously

### SQLite
- **Single-Node Only**: SQLite lacks advisory lock mechanism
- **Node Restart Support**: Cleanup resumes automatically after restart
- **Not Recommended**: For multi-replica deployments, use PostgreSQL

## Configuration Guidelines

### Default Values
- **Enabled**: `false` (must be explicitly enabled)
- **TTL**: 24 hours (ensures tokens are truly finalized)
- **Scan Interval**: 1 hour (less aggressive than recovery's 5 seconds)
- **Batch Size**: 100 tokens per sweep
- **Worker Count**: 1 parallel worker (conservative default)
- **Instance ID**: Auto-generated as `cleanup-<pointer>` if not provided

### Tuning Recommendations

1. **For High-Volume Environments:**
   - Increase `batchSize` to 200-500 for more tokens per sweep
   - Increase `workerCount` to 4-16 for faster parallel processing
   - Decrease `scanInterval` to 30m for more frequent cleanup

2. **For Resource-Constrained Systems:**
   - Keep `workerCount` at 1 to minimize CPU usage
   - Increase `scanInterval` to 2-4h to reduce database load
   - Keep default `batchSize` to limit memory usage

3. **For Security-Sensitive Deployments:**
   - Decrease `ttl` to 12h for faster key removal
   - Decrease `scanInterval` to 30m for more frequent cleanup
   - Monitor cleanup metrics to ensure timely processing

4. **For Multi-Instance Deployments:**
   - **PostgreSQL Required**: Multi-instance deployments require PostgreSQL for distributed coordination
   - Consider setting explicit `instanceID` values for easier debugging and monitoring

### Performance Considerations
- Each scan queries the token database, so `scanInterval` directly affects database load
- `workerCount` affects CPU utilization during cleanup sweeps
- `batchSize` affects memory usage and the duration of each cleanup sweep
- The relationship `scanInterval < ttl` ensures timely cleanup without premature processing

## Monitoring

Key metrics to monitor:

- **Cleanup Rate**: Tokens cleaned per hour
- **Backlog Size**: Number of tokens eligible for cleanup
- **Error Rate**: Failed cleanup attempts (check logs for details)
- **Leadership Changes**: Frequency of leader election (should be stable)
- **Processing Time**: Duration of each cleanup sweep
- **Retried Tokens**: Tokens that failed at least one key deletion and are still pending. A token
  stuck here across many sweeps means a key deletion is failing persistently, not transiently

**Log Levels:**
- `INFO`: Successful cleanup operations, manager start/stop
- `WARN`: Failed key deletions, key not found, leadership issues
- `DEBUG`: Detailed sweep information, SKI derivation, leadership acquisition

## Security Considerations

- **TTL Safety**: 24-hour default ensures tokens are finalized before key deletion
- **Idempotency**: Safe to retry cleanup operations
- **Audit Trail**: `token_ski_cleanups` table provides cleanup history with timestamps and instance tracking
- **Key Isolation**: Only deletes keys for deleted tokens, never active tokens
- **All-or-Nothing Marking**: A token is only recorded as cleaned once *all* of its keys are gone,
  so a failed deletion can never be silently forgotten
- **X.509 Exclusion**: X.509 key material is never deleted by design — a `token_ski_cleanups` row
  for an X.509-owned token means "nothing to clean". See
  [X.509 is intentionally out of scope](#x509-is-intentionally-out-of-scope)
- **Instance Tracking**: `cleaned_by` field records which instance performed cleanup

## Comparison with Recovery Service

| Feature | Recovery Service | Cleanup Service |
|---------|-----------------|-----------------|
| **Purpose** | Re-register finality listeners | Delete stale cryptographic keys |
| **Frequency** | Every 5 seconds | Every 1 hour |
| **TTL** | 30 seconds | 24 hours |
| **Target** | Pending transactions | Deleted tokens |
| **Urgency** | High (affects finality) | Low (housekeeping) |
| **Batch Size** | 100 | 100 |
| **Workers** | 4 | 1 (default) |
| **Initial Sweep** | Immediate | Immediate |
| **Leadership** | Per sweep | Per sweep |

## See Also

- [Storage Service](../storage.md) - Database operations and interfaces
- [Configuration Guide](../../configuration.md) - Detailed configuration parameters
- [Transaction Recovery Service](recovery.md) - Related recovery mechanism
- [Identity Services](../identity.md) - Identity management and SKI derivation
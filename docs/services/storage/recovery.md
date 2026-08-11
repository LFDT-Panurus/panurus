# Transaction Recovery Service

The **Transaction Recovery Service** provides automatic re-registration of finality listeners for pending transactions that may have lost their listeners due to node restarts, network interruptions, or other failures. This ensures that transactions eventually reach finality even after system disruptions.

## Architecture

The recovery system consists of three main components:

1. **Manager**: Orchestrates the recovery process with periodic scanning and distributed coordination
2. **Handler**: Implements the actual recovery logic for individual transactions
3. **Storage**: Provides database operations for claiming and tracking recovery state

## Components

### Recovery Manager

The Manager runs in the background and periodically scans for pending transactions that are eligible for recovery. It uses distributed locking (PostgreSQL advisory locks) to ensure only one replica in a multi-instance deployment performs recovery at a time.

**Key features:**
- Configurable scan intervals and batch sizes
- Worker pool for parallel transaction processing
- Lease-based claim mechanism to prevent duplicate work
- Graceful shutdown with proper cleanup

### Recovery Handler

The Handler interface defines how individual transactions are recovered. The TTX service provides a concrete implementation (`TTXRecoveryHandler`) that:
- Queries transaction status from the network
- Applies finality logic (Valid/Invalid/Busy)
- Updates local database state
- Handles hash verification and token request processing

### Storage Interface

The Storage interface abstracts database operations needed for recovery:
- `AcquireRecoveryLeadership`: Obtains distributed lock for leader election
- `ClaimPendingTransactions`: Atomically claims a batch of pending transactions, returning a lightweight `RecoveryClaim` (`TxID` + `StoredAt`) for each row — the recovery loop only needs these two fields, so the SQL projection is kept narrow
- `ReleaseRecoveryClaim`: Releases claim after processing
- `SetStatus`: Promotes a transaction to a terminal status. Used by the recovery loop to mark `NotFound`-past-grace-period rows as `Orphan` so they exit the eligible scan range without being conflated with ledger-rejected transactions (`Deleted`)

## Database Support

### PostgreSQL (Recommended for Production)

PostgreSQL is the recommended database for production multi-instance deployments:
- Advisory locks provide distributed coordination
- Atomic `UPDATE...RETURNING` ensures no duplicate claims
- Supports horizontal scaling with multiple replicas
- Leader election prevents conflicting recovery attempts

### SQLite (Development and Single-Node)

SQLite is supported for single-node deployments and development:
- Handles node restarts gracefully
- Simpler setup for development environments
- Not designed for multi-replica scenarios
- No distributed locking mechanism

## Configuration

Recovery behavior is controlled via configuration (see [Configuration](../../configuration.md)):

```yaml
recovery:
  enabled: true              # Enable/disable recovery
  ttl: 30s                   # Minimum age before recovery
  scanInterval: 5s           # How often to scan
  batchSize: 100             # Max transactions per scan
  workerCount: 4             # Parallel workers
  leaseDuration: 30s         # Claim lease duration
  advisoryLockID: 8389...    # PostgreSQL lock ID
  instanceID: ""             # Instance identifier
  notFoundGracePeriod: 30m   # Promote NotFound rows to Orphan after this age (0 disables)
```

## Usage Example

Creating a recovery manager:

```go
config := recovery.Config{
    Enabled:             true,
    TTL:                 30 * time.Second,
    ScanInterval:        5 * time.Second,
    BatchSize:           100,
    WorkerCount:         4,
    LeaseDuration:       30 * time.Second,
    AdvisoryLockID:      8389190333894887286,
    NotFoundGracePeriod: 30 * time.Minute,
}

manager := recovery.NewManager(
    logger,
    storage,  // Implements Storage interface
    handler,  // Implements Handler interface
    config,
)

// Start recovery
if err := manager.Start(); err != nil {
    return err
}
defer manager.Stop()
```

## Implementing a Custom Handler

To implement a custom recovery handler:

```go
type MyHandler struct {
    // your dependencies
}

func (h *MyHandler) Recover(ctx context.Context, txID string) error {
    // 1. Query transaction status from your backend
    // 2. Apply finality logic based on status
    // 3. Update local database state
    // 4. Return nil on success, error on failure
    return nil
}
```

## Recovery Process Flow

1. Manager acquires leadership (PostgreSQL advisory lock)
2. Manager queries for pending transactions older than TTL
3. Manager atomically claims a batch of transactions, each returned as a `RecoveryClaim` (`TxID` + `StoredAt`)
4. Manager distributes claimed transactions to worker pool
5. Each worker calls `Handler.Recover()` for its transactions
6. Handler queries network and applies finality logic
7. If the handler reports `NotFound` and the row was stored more than `notFoundGracePeriod` ago, the manager promotes the row to `Orphan` via `SetStatus` so it exits the eligible scan range
8. Manager releases claims with success/failure message
9. Process repeats on next scan interval

## Transaction Status Lifecycle

A token request transitions through the following statuses as the recovery loop interacts with it:

- **Pending**: The transaction has been submitted but its finality is not yet known. Only rows in this status are eligible for `ClaimPendingTransactions`; the claim query and its supporting partial index filter on `status = Pending`.
- **Confirmed**: The transaction has been validated by the ledger and committed locally. Terminal.
- **Deleted**: The transaction was actively rejected — either by the ledger (`network.Invalid`) or by local validation (token request hash mismatch via the finality listener). Terminal.
- **Orphan**: The transaction never reached the ledger — the recovery loop saw a persistent `NotFound` from the network past `notFoundGracePeriod`. Terminal in this version, and intentionally distinct from `Deleted` so operators (and future replay tooling) can identify broadcast failures separately from ledger-rejected transactions.

All three terminal statuses (`Confirmed`, `Deleted`, `Orphan`) are excluded from subsequent recovery sweeps by virtue of the `status = Pending` filter on the claim query.

## Error Handling

- **Transient errors** (Busy status): Released gracefully, retried on next scan
- **Permanent errors** (Invalid tx): Marked as `Deleted` in the database
- **Orphan transactions** (persistent `NotFound` past `notFoundGracePeriod`): Marked as `Orphan` to indicate the transaction never reached the ledger; distinct from `Deleted` so operators can distinguish broadcast failures from ledger-rejected transactions
- **Handler errors**: Logged individually, claim released with error message
- **Network errors**: Propagated to caller, claim released for retry

## Performance Tuning

### For High-Throughput Environments
- Increase `batchSize` (200-500)
- Increase `workerCount` (8-16)
- Decrease `scanInterval` (2-3s)

### For Resource-Constrained Environments
- Decrease `batchSize` (50)
- Decrease `workerCount` (2)
- Increase `scanInterval` (10-15s)

### For Long-Running Transaction Assembly
- Increase `ttl` (60s or more)
- Ensure `leaseDuration` > expected processing time

## Thread Safety

The Manager is thread-safe and can be safely started/stopped from multiple goroutines. The Handler implementation must also be thread-safe as it will be called concurrently by multiple workers.
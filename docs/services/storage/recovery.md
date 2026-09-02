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
  batchSize: 16              # Max transactions per scan
  workerCount: 8             # Parallel workers
  leaseDuration: 5m          # Claim lease duration
  transactionTimeout: 60s    # Deadline for a single transaction's recovery attempt (0 = unbounded, or >= 10s)
  instanceID: ""             # Instance identifier
  notFoundGracePeriod: 30m   # Promote NotFound rows to Orphan after this age (0 disables)
  stuckTransactionAlertThreshold: 5  # Escalate to Error log after this many consecutive timeouts (0 disables)
```

## Usage Example

Creating a recovery manager:

```go
config := recovery.Config{
    Enabled:                        true,
    TTL:                            30 * time.Second,
    ScanInterval:                   5 * time.Second,
    BatchSize:                      16,
    WorkerCount:                    8,
    LeaseDuration:                  5 * time.Minute,
    TransactionTimeout:             60 * time.Second,
    NotFoundGracePeriod:            30 * time.Minute,
    StuckTransactionAlertThreshold: 5,
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

1. Manager acquires leadership (PostgreSQL advisory lock). Normally released at the end of each sweep, but held across sweeps for as long as any transaction is still abandoned in the background from a previous sweep (see step 5): this instance's own next `ClaimPendingTransactions` call is what keeps that transaction's claim lease alive (step 5), and that only reliably happens if this instance keeps winning leadership, rather than leaving it to the next non-blocking lock attempt succeeding by chance. `Stop()` always releases leadership when called, regardless of whether anything is still abandoned
2. Manager queries for pending transactions older than TTL
3. Manager atomically claims a batch of transactions, each returned as a `RecoveryClaim` (`TxID` + `StoredAt`)
4. Manager distributes claimed transactions to worker pool
5. Each worker calls `Handler.Recover()` for its transactions, bounded by `transactionTimeout`: the call runs in its own goroutine, and the worker moves on as soon as the deadline fires rather than waiting for the call to return, so a peer that hangs without honouring the context (as some ledger calls do) still cannot block the sweep indefinitely, and since leadership is held for as long as anything is abandoned (step 1), neither can every other replica in the meantime. The abandoned goroutine keeps running in the background until the underlying call eventually completes; its claim stays held (not released) in the meantime, and its eventual result is used, not discarded (see step 8). If the same transaction is still claimed and re-dispatched to a worker before that abandoned goroutine returns, the manager skips it rather than starting a second concurrent `Recover` call for it
6. Handler queries network and applies finality logic
7. If the handler reports `NotFound` and the row was stored more than `notFoundGracePeriod` ago, the manager promotes the row to `Orphan` via `SetStatus` so it exits the eligible scan range, unless this is a step-5 background finalization arriving after `Stop()` has already run, in which case the promotion is skipped: leadership was released by `Stop()` (step 1), so another replica may have since legitimately resolved the same transaction, and an unconditional `SetStatus` could overwrite that outcome
8. Manager releases each claim with a success/failure message, as soon as a final result for it is known: synchronously, for a `Recover()` call that returns within `transactionTimeout`, or later in the background, for one that did not (step 5), whichever comes first, and only once. The in-flight guard for a transaction is cleared only after this release actually completes, not before, so a sweep that reclaims the same transaction in between cannot start a second attempt out from under the release still in progress
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

### Transaction Timeout
- `transactionTimeout` bounds a single `Handler.Recover()` call (ledger status query plus finality logic), not the whole sweep. It is not a general no-stall guarantee for the sweep: `ClaimPendingTransactions`, `ReleaseRecoveryClaim`, and `SetStatus` still run on the sweep's own context, which has no deadline, so a wedged database connection in any of those can still stall recovery on every replica of the TMS, the same failure mode `transactionTimeout` was added to fix on the ledger side
- That per-transaction bound does not by itself bound the sweep: a batch of `batchSize` claims split across `workerCount` workers, each taking the full `transactionTimeout`, takes up to `(batchSize / workerCount) × transactionTimeout` worst case. The invariant to size `leaseDuration` against is `leaseDuration > (batchSize / workerCount) × transactionTimeout`. With the shipped defaults (`batchSize: 16`, `workerCount: 8`, `transactionTimeout: 60s`) that worst case is `2 × 60s = 120s`, comfortably inside the default `leaseDuration: 5m`. The manager Warns at startup, rather than refusing to start, when this does not hold: an attempt still in flight when this happens keeps its claim rather than releasing it (see the "stuck transaction" bullet below), so this is a throughput concern, not a correctness one
- `leaseDuration` should also comfortably exceed `scanInterval`. A transaction still recovering past its `transactionTimeout` keeps its claim alive only because this instance's own next sweep reclaims the same still-`Pending` row, which refreshes the claim's lease as a side effect, and this instance holds recovery leadership across sweeps for as long as that transaction stays abandoned specifically so that reclaim keeps happening on schedule (see step 1 of the process flow above) rather than depending on this instance winning leadership again by chance. If `scanInterval` is not shorter than `leaseDuration`, that renewal can still arrive too late and the claim can lapse between this instance's own sweeps. The manager Warns at startup when this does not hold
- Raise it if legitimate recoveries (e.g. large token requests re-verified from the database) routinely take longer than the default 60s; lower it if a slow or flaky peer should be given up on sooner, down to a 10s floor: an explicit `transactionTimeout` below 10s is rejected when the configuration is loaded, since a deadline that tight is likely to abandon recoveries that were merely slow rather than genuinely stuck. Setting it to `0` disables the per-transaction deadline entirely, but that also removes the only thing that can interrupt a `Recover` call that ignores its context: `Stop()` itself can then block indefinitely on a hung call, and every later `Start`/`Stop` wedges behind it. The manager logs a Warn at startup when the timeout is disabled; it does not stop you from doing it
- A transaction that keeps timing out stays `Pending` and is re-claimed on every sweep, but the manager will not start a second concurrent `Recover` call for it while an earlier attempt is still running past its own `transactionTimeout`: it skips the attempt instead. Unlike earlier versions of this manager, the claim is **not** released when this happens: releasing it while the earlier attempt might still be running is what let a second replica claim and run a concurrent `Recover` (and `Commit`) against the same transaction. The claim stays held, kept alive by the leadership-holding and reclaim-renewal described above, until the original stuck attempt actually returns, at which point the manager finalizes it in the background, releasing the claim with the real outcome and only then clearing the in-flight guard, whenever that turns out to be, including well after the sweep (or even the process's `Stop()` call) that first hit the timeout has already moved on. A `recoverCtx.Done()` firing because `Stop()` cancelled the manager, rather than because `transactionTimeout` actually elapsed, is not counted or alerted on as a timeout: it is an ordinary shutdown catching an attempt mid-flight, not evidence the transaction itself is stuck
- `stuckTransactionAlertThreshold` escalates the per-attempt failure log from Warn to Error once the same transaction has timed out this many times in a row, so a persistently-unresponsive peer is visible to alerting instead of blending into routine sweep failures. Past the threshold the log backs off by doubling (threshold, `2×`, `4×`, `8×`, ...) rather than firing every sweep, so a transaction stuck for hours does not flood alerting. It is purely a log-severity signal: a timeout means the status query didn't answer in time, not that the transaction failed. Unlike a persistent `NotFound`, the manager does not promote it to `Orphan`. Set to `0` to disable the escalation

## Thread Safety

The Manager is thread-safe and can be safely started/stopped from multiple goroutines. The Handler implementation must also be thread-safe as it will be called concurrently by multiple workers.

## Shutdown Behaviour

`Stop()` cancels the manager context and waits for the recovery loop to return. If a sweep is mid-batch, the fan-out to the worker pool aborts on cancellation: workers exit as soon as they observe the cancellation, and any claims not yet dispatched are simply left undispatched. Those rows keep their `Pending` status, so they become eligible again once their lease (`leaseDuration`) expires and are picked up by the next sweep — on this or another replica. The aborted sweep reports a `recovery fan-out cancelled` error. Because this is an ordinary shutdown rather than a failure, the loop logs it at debug level and only warns for genuine sweep errors. The sweep summary counts successes against the claims actually dispatched, so a partial sweep logs `claimed=N, dispatched=M, ...` at warn level instead of crediting the undispatched tail as succeeded.

With a `transactionTimeout` configured, a worker mid-`Handler.Recover()` does not wait out that call on shutdown: it abandons the call once the deadline fires and moves on (see [Transaction Timeout](#transaction-timeout)), so `Stop()` returning does **not** guarantee every in-flight recovery attempt has actually stopped. An abandoned call can keep running after `Stop()` returns and eventually reach `Commit`, writing into a store the SDK may already be tearing down as part of node shutdown. This is the same accepted tradeoff the timeout mechanism makes everywhere else: bounding the worker takes priority over waiting for a ctx-blind call to finish. With `transactionTimeout: 0` there is no deadline to abandon the call at, so `Stop()` instead blocks on `Handler.Recover()` directly, potentially indefinitely if the handler ignores its context.

The claim on a transaction abandoned this way is not released by `Stop()` either: it is released only once the abandoned call actually returns and the manager finalizes it in the background, which can be well after `Stop()` has already returned. That finalization uses its own independent context rather than the (by then long since cancelled) sweep or manager context, specifically so the release can still succeed after shutdown.

Recovery leadership, unlike the claim above, is always released by `Stop()`, even if a transaction is still abandoned in the background: this instance can no longer productively use it once its own sweep loop has exited, and holding onto it would only block a live peer that could otherwise take over. That means a transaction still abandoned when `Stop()` runs is no longer guaranteed to have its lease renewed by this instance, and a live peer can legitimately reclaim and finish it while this instance's own attempt is still finishing up in the background. For that reason, if the background finalization's handler eventually reports the transaction as `NotFound`, the manager does not promote it to `Orphan` once it observes that `Stop()` already ran: another replica may have since resolved it to something else, and unlike the owner-scoped claim release, `SetStatus` has no way to tell that has happened.
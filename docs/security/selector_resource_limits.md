# Selector Resource Limits - Security Guide

> **Performance Note**: Database-level LIMIT optimization has been implemented to reduce I/O overhead.


## Overview

The Panurus selector service implements hard resource limits to prevent algorithmic attacks that could exhaust system resources (CPU, memory, storage) through maliciously crafted token selection requests.

## Threat Model

### Attack Vector

An attacker can craft token selection requests that:
1. **Iterate through millions of tokens** - Exhausting CPU and memory
2. **Attempt excessive lock acquisitions** - Creating lock contention storms
3. **Trigger infinite retry loops** - Blocking the system indefinitely
4. **Accumulate unbounded locks** - Exhausting lock storage
5. **Consume unlimited wall-clock time** - Preventing other operations

### Why This Matters

Token selection happens **before blockchain consensus**, meaning:
- **No transaction fees** - Attacker doesn't pay for failed selections
- **Pure DoS vector** - Can exhaust resources without valid transactions
- **Amplification** - One malicious request → millions of operations
- **Cascading failure** - Resource exhaustion affects all users
- **Limited audit trail** - Failed selections may not be comprehensively logged

## Resource Limits

### 1. Token Iteration Depth Limit

**What it limits**: Maximum number of tokens examined during selection

**Default**: 10,000 tokens

**Configuration**:
```yaml
token:
  selector:
    limits:
      maxTokensPerSelection: 10000
```

**When it triggers**: When the selector examines more than the configured number of tokens

**Error message**: `"token selection aborted: exceeded max token iteration limit (10000 tokens)"`

**Database optimization**: This limit is enforced at **two levels**:
1. **Database query level**: SQL queries include `LIMIT ?` clause to prevent fetching excess rows
2. **Application level**: Iterator counter provides defense-in-depth safety check

**Performance impact**: Database-level LIMIT significantly reduces I/O overhead:
- Without LIMIT: Database may scan millions of rows, returning them one-by-one
- With LIMIT: Database stops after returning configured number of rows
- Result: ~10x faster query execution for large token sets

**Tuning guidance**:
- **Increase** if legitimate operations require examining more tokens
- **Decrease** for tighter security in low-throughput environments
- **Monitor** actual token counts in production before adjusting

### 2. Lock Acquisition Attempt Limit

**What it limits**: Maximum number of lock operations attempted during selection

**Default**: 50,000 attempts (5x token iteration limit)

**Configuration**:
```yaml
token:
  selector:
    limits:
      maxLockAttempts: 50000
```

**When it triggers**: When the selector attempts more lock operations than configured

**Error message**: `"token selection aborted: exceeded max lock attempts (50000) after examining X tokens"`

**Tuning guidance**:
- Should be ≥ `maxTokensPerSelection` (validation enforced)
- Higher values allow for more lock contention tolerance
- Set to 5-10x `maxTokensPerSelection` for high-concurrency environments

### 3. Retry Cycle Limit

**What it limits**: Maximum number of outer retry loops during selection

**Default**: 10 cycles

**Configuration**:
```yaml
token:
  selector:
    limits:
      maxRetryCycles: 10
```

**When it triggers**: When the selector retries more times than configured

**Error message**: `"token selection aborted: exceeded max retry cycles (10) after examining X tokens and Y lock attempts"`
(the simple selector reports `exceeded max retries (10) ...` and wraps the typed reason
the selection failed — `token.SelectorSufficientButLockedFunds`,
`token.SelectorSufficientFundsButConcurrencyIssue`, or `token.SelectorInsufficientFunds`
— so callers can still switch on the sentinel)

**Tuning guidance**:
- **Increase** in high-contention environments where retries are common
- **Decrease** for faster failure in low-contention environments
- Balance between resilience and attack mitigation

### 4. Lock Store Growth Limit

**What it limits**: Maximum number of locks a single transaction can hold

**Default**: 5,000 locks

**Configuration**:
```yaml
token:
  selector:
    limits:
      maxLocksPerTransaction: 5000
```

**When it triggers**: When a transaction tries to acquire more locks than configured

**Error message**: `"lock limit exceeded: transaction TX already holds 5000 locks (max: 5000)"`,
wrapping `token.SelectorRateLimited` so the selection aborts immediately instead
of treating the denial as contention and retrying.

The count is tracked per transaction for the whole life of that transaction: it
is released when the transaction's locks are released (`UnlockByTxID`) or when
its selector is closed, and stale counters are reclaimed once a transaction has
been idle longer than `leaseExpiry`. Periodic lock cleanup does **not** reset
live counters, so the ceiling cannot be circumvented by waiting for a cleanup
tick.

**Tuning guidance**:
- Should be ≤ `maxTokensPerSelection` (validation enforced)
- **Increase** for bulk operations that legitimately need many locks
- **Decrease** to reduce memory footprint per transaction

### 5. Wall-Clock Timeout

**What it limits**: Maximum time allowed for entire selection operation

**Default**: 30 seconds *plus* the worst-case retry budget, i.e.
`30s + maxRetryCycles * retryInterval`. With the default 10 retry cycles and a
5s `retryInterval` the effective default is 80s.

The retry budget is added because both selectors sleep for up to
`retryInterval` between cycles: a fixed 30s ceiling would expire while the
retries that are meant to resolve contention are still in progress, turning
ordinary contention into a timeout that the caller cannot resolve by retrying.
An explicitly configured `selectionTimeout` is always used as-is.

A non-positive value means **no timeout**.

**Configuration**:
```yaml
token:
  selector:
    limits:
      selectionTimeout: 30s
```

**When it triggers**: When selection takes longer than configured timeout

**Error message**: `"token selection aborted: exceeded timeout (30s) after examining X tokens and Y lock attempts"`,
wrapping the `token.SelectorTimedOut` sentinel so callers can distinguish a
timeout from a permanent failure.

**Tuning guidance**:
- **Increase** for slow databases or bulk operations
- **Decrease** for faster failure detection
- Keep it above `maxRetryCycles * retryInterval`, or the timeout will fire
  before the retry budget is spent
- Consider database query performance when setting

## Configuration Examples

### Default Secure Configuration

```yaml
token:
  selector:
    limits:
      maxTokensPerSelection: 10000      # 10k tokens ≈ 1MB memory
      maxLockAttempts: 50000            # 5x iteration limit
      maxRetryCycles: 10                # Reasonable for transient issues
      maxLocksPerTransaction: 5000      # Half of iteration limit
      selectionTimeout: 30s             # Generous for legitimate use
```

### High-Throughput Environment

```yaml
token:
  selector:
    limits:
      maxTokensPerSelection: 50000      # More tokens for bulk ops
      maxLockAttempts: 250000           # Proportionally higher
      maxRetryCycles: 20                # More retries for high contention
      maxLocksPerTransaction: 25000     # Proportionally higher
      selectionTimeout: 120s            # Longer timeout for bulk ops
```

### Low-Latency Environment

```yaml
token:
  selector:
    limits:
      maxTokensPerSelection: 5000       # Fewer tokens for faster failure
      maxLockAttempts: 25000            # Proportionally lower
      maxRetryCycles: 5                 # Fewer retries for faster failure
      maxLocksPerTransaction: 2500      # Proportionally lower
      selectionTimeout: 10s             # Shorter timeout
```

## Monitoring and Alerting

### Key Metrics to Monitor

1. **Limit Violations**
   - Track frequency of each limit type being hit
   - Alert on sudden increases in violations

2. **Resource Usage**
   - Monitor actual tokens examined per selection
   - Track lock attempt counts
   - Measure selection duration

3. **Success Rate**
   - Track ratio of successful vs. aborted selections
   - Alert on drops in success rate

### Recommended Alerts

```yaml
# Alert when limit violations exceed threshold
- alert: HighSelectorLimitViolations
  expr: rate(selector_limit_violations_total[5m]) > 10
  annotations:
    summary: "High rate of selector limit violations"
    description: "{{ $value }} limit violations per second"

# Alert when selection success rate drops
- alert: LowSelectorSuccessRate
  expr: rate(selector_success_total[5m]) / rate(selector_attempts_total[5m]) < 0.9
  annotations:
    summary: "Selector success rate below 90%"
```

## Operational Procedures

### Responding to Limit Violations

1. **Investigate the cause**
   - Check logs for patterns in aborted selections
   - Identify if it's legitimate load or attack

2. **Temporary mitigation**
   - If legitimate: Increase relevant limits
   - If attack: Block malicious actors at network level

3. **Long-term resolution**
   - Optimize token distribution to reduce selection complexity
   - Implement rate limiting at application level
   - Consider sharding or partitioning strategies

### Tuning Process

1. **Baseline measurement**
   - Run in production with default limits
   - Collect metrics for 1-2 weeks

2. **Analysis**
   - Calculate 99th percentile for each resource
   - Add 50% safety margin

3. **Gradual adjustment**
   - Increase limits incrementally
   - Monitor impact on system resources
   - Validate no degradation in performance

4. **Documentation**
   - Document rationale for custom limits
   - Record baseline metrics used for tuning

## Security Best Practices

1. **Never disable limits** - All limits are enforced by default
2. **Start conservative** - Use default values initially
3. **Monitor continuously** - Track metrics and violations
4. **Tune based on data** - Use real production metrics for adjustments
5. **Document changes** - Record why limits were adjusted
6. **Review regularly** - Reassess limits as usage patterns change
7. **Test thoroughly** - Validate limits in staging before production

## Validation Rules

The configuration system enforces these relationships:

- `maxLockAttempts` ≥ `maxTokensPerSelection`
- `maxLocksPerTransaction` ≤ `maxTokensPerSelection`
- All limits must be positive integers
- Timeout must be positive duration

Invalid configurations will be rejected at startup with clear error messages.

## Pluggable Rate Limiting

Token selection acquires a short-lived *lock* on each candidate token so that two
concurrent transactions do not try to spend the same token. Under load, a single
wallet can drive a large number of selection/lock requests. To protect the lock store,
to enforce fairness between wallets, or to integrate with an existing quota system,
Panurus offers two ways to throttle this:

The hard limits described above bound the work of a *single* selection. They do not
bound the *rate* at which a wallet may issue selections. For that, Panurus offers:

1. A **built-in per-wallet rate limiter**, activated purely from configuration or from
   code. It is a token bucket per wallet, in process, and it is **disabled by default**.
2. A **fail-fast contract**, `token.SelectorRateLimited`, plus a **wallet-id-aware lock
   function**. Both selector drivers (simple and sherdlock) pass the wallet id the tokens
   are being selected for into the `Locker`'s lock function, and abort the selection
   immediately when a lock is denied with an error wrapping that sentinel. This is the
   integration point for applications that would rather reuse the rate-limiting
   infrastructure they already run (for example a Redis-backed limiter shared across
   processes).

## The built-in limiter

Package `token/services/selector/ratelimit`. One **selection request** — one
`Selector.Select` call — costs one unit from the bucket of the wallet it selects for,
regardless of how many tokens the request ends up locking or how many times the selector
retries internally on contention. Deliberately *not* per token lock attempt: charging
there would make a large transfer cost more than a small one and would let the selector's
own contention retries drain a wallet's allowance.

Properties worth knowing:

- **Per wallet, per TMS.** Buckets are keyed by the wallet id *and* the TMS id, so the
  same wallet id in two networks or namespaces gets two independent allowances.
- **Bounded memory.** Buckets are created on first use and pruned without any background
  goroutine: idle ones are swept during ordinary access, and a hard cap
  (`rateLimitMaxBuckets`) evicts the least recently used ones if the sweep is not enough.
- **Unlocking is never throttled.** Only `Select` is metered; `Unlock` and `Close` pass
  through, so a throttled wallet can always clean up after itself.
- **Empty wallet ids are never throttled.**
- **Nothing is leaked on a denial.** The request is rejected before the selector runs, so
  no token is locked and there is nothing to release.

### Enabling it from configuration

Under `token.selector` (see [../configuration.md](../configuration.md)):

```yaml
token:
  selector:
    driver: sherdlock
    # Enables the limiter with the defaults below.
    rateLimitEnabled: true
    # Selection requests per second per wallet. A positive value implies rateLimitEnabled: true.
    # Defaults to 100.
    rateLimit: 100
    # Back-to-back requests allowed to one wallet. Defaults to 2 × rateLimit, so 200.
    rateLimitBurst: 200
    # Maximum number of per-wallet buckets held in memory. Defaults to 4096.
    rateLimitMaxBuckets: 4096
```

Omitting all four keys, which is the default, leaves selection unmetered.

### Enabling it from code

Both selector services accept `ratelimit.Option` values, which take precedence over the
configuration:

```go
import (
    "github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
    "github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
)

// The built-in limiter with its defaults (100 requests/s per wallet, burst 200).
svc := sherdlock.NewService(fetcherProvider, lockStoreManager, configProvider, metricsProvider,
    ratelimit.WithDefaultLimiter())

// Explicit rate and burst.
svc = sherdlock.NewService(fetcherProvider, lockStoreManager, configProvider, metricsProvider,
    ratelimit.WithLimiter(ratelimit.New(ratelimit.Config{Rate: 20, Burst: 40})))

// Explicitly off, whatever the configuration says.
svc = sherdlock.NewService(fetcherProvider, lockStoreManager, configProvider, metricsProvider,
    ratelimit.WithLimiter(nil))
```

`simple.NewService(lockerProvider, configProvider, opts ...ratelimit.Option)` takes the
same options.

A limiter passed with `WithLimiter` belongs to the caller: the service never stops it, not
even from `Shutdown`. That matters because `Shutdown` also runs on routine public-parameter
reloads, and resetting every wallet's bucket there would let a throttled client wash out
its debt. `BucketLimiter.Stop()` exists for callers that own a limiter and want its memory
back.

### Supplying your own limiter

`WithLimiter` accepts any implementation of:

```go
// Limiter meters token selection requests per wallet.
type Limiter interface {
    // Allow returns nil when a selection request for walletID within scope may proceed,
    // and an error wrapping token.SelectorRateLimited when it must be denied.
    // scope is the TMS id. An empty walletID is never throttled.
    Allow(ctx context.Context, scope string, walletID string) error
}
```

This is the simplest way to plug in a shared, cluster-wide limiter (Redis, a quota table,
a sidecar) while keeping the metering point — one unit per selection request — and the
fail-fast behaviour that Panurus already implements.

## The fail-fast contract

`token/selector.go` defines:

```go
// SelectorRateLimited is the contract error returned (directly or wrapped) to deny a
// selection for policy reasons such as rate limiting or quota.
var SelectorRateLimited = errors.New("selection rate limit exceeded")
```

When a `Locker` returns an error `e` with `errors.Is(e, token.SelectorRateLimited)`, the
selector:

- stops iterating candidate tokens,
- releases any tokens it already locked for this request, and
- returns `e` to the caller.

Any *other* error from the lock function keeps the existing semantics: the token is
treated as unavailable (e.g. already locked by another transaction) and selection
continues / retries as before.

### The lock function

Both selector drivers route through a `Locker` whose lock function receives the
wallet id, so a custom `Locker` can apply per-wallet policies of its own — per token
lock rather than per selection request.

**Simple selector** — `token/services/selector/simple/selector.go`:

```go
type Locker interface {
    // Lock locks the token id for the consumer transaction txID on behalf of the given
    // owner (the wallet the tokens are selected for, ownerFilter.ID()). Return an error
    // wrapping token.SelectorRateLimited to deny the lock and make the selection fail fast.
    Lock(ctx context.Context, owner string, id *token.ID, txID string, reclaim bool) (string, error)
    UnlockIDs(ctx context.Context, owner string, ids ...*token.ID) []*token.ID
    UnlockByTxID(ctx context.Context, txID string)
    IsLocked(id *token.ID) bool
}
```

**Sherdlock selector** — the lock store it drives is
`token/services/storage/db/driver/token.go` `TokenLockStore` (also exposed as
`sherdlock.Locker`):

```go
type TokenLockStore interface {
    common.DBObject
    // Lock locks tokenID for consumerTxID on behalf of walletID. A custom store may use
    // walletID to throttle per wallet, returning an error wrapping token.SelectorRateLimited.
    Lock(ctx context.Context, tokenID *token.ID, consumerTxID transaction.ID, walletID string) error
    UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error
    Cleanup(ctx context.Context, leaseExpiry time.Duration) error
}
```

The built-in in-memory locker and the SQL-backed `TokenLockStore` accept `walletID`
but do not act on it — they apply no per-wallet rate limiting or quota. They do
enforce the per-transaction `maxLocksPerTransaction` ceiling described above, and
report it through this same contract.

### Integrating your own rate limiting in a Locker

Provide a `Locker` that wraps the SDK's default locker and enforces your policy before
delegating. Below, a Redis-backed limiter throttles per wallet; the same shape works
for an in-process limiter, a quota table, etc. Note that this is charged **per token lock
attempt**, unlike the built-in limiter above.

#### Simple selector

```go
import (
    "context"

    "github.com/LFDT-Panurus/panurus/token"
    "github.com/LFDT-Panurus/panurus/token/services/selector/simple"
    tokenapi "github.com/LFDT-Panurus/panurus/token/token"
    "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// rateLimitedLocker decorates the default simple.Locker with per-wallet throttling.
type rateLimitedLocker struct {
    simple.Locker            // embeds the SDK's default locker
    limiter       RedisLimiter // your existing infrastructure
}

func (l *rateLimitedLocker) Lock(ctx context.Context, owner string, id *tokenapi.ID, txID string, reclaim bool) (string, error) {
    if !l.limiter.Allow(ctx, owner) {
        return "", errors.Wrapf(token.SelectorRateLimited, "wallet %s throttled", owner)
    }

    return l.Locker.Lock(ctx, owner, id, txID, reclaim)
}
```

Wire it in by providing a `simple.LockerProvider` whose `New` returns your decorator
instead of the default `inmemory.NewLocker`.

#### Sherdlock selector

Provide a `TokenLockStore` (via the `tokenlockdb.StoreServiceManager` used by
`sherdlock.NewService`) whose `Lock` enforces the limit before delegating to the
SQL-backed store, returning an error wrapping `token.SelectorRateLimited` when a wallet
is throttled.

### Handling the error

Callers should treat `token.SelectorRateLimited` as a transient, retryable-later
condition rather than an insufficient-funds error:

```go
ids, sum, err := selector.Select(ctx, ownerFilter, amount, tokenType)
if errors.Is(err, token.SelectorRateLimited) {
    // back off and retry later, shed the request, or surface a 429-style response
}
```

The built-in limiter's error message states how long to wait before the wallet has a
request available again.

### Notes

- Passing an empty `walletID` is valid; a `Locker` that keys its policy on wallet id
  should treat empty as "no throttling" (the default lockers ignore it entirely, and so
  does the built-in limiter).
- The built-in limiter is per process. If a wallet's traffic is spread over several nodes,
  each node enforces its own allowance; supply a shared `Limiter` if you need a
  cluster-wide budget.
- Because a policy implemented in your own `Locker` lives entirely in your code, its scope
  (per process vs shared across a cluster), persistence, and lifecycle are under your
  control.

## Migration Guide

### For Existing Deployments

1. **Review current usage**
   - Analyze logs for typical selection patterns
   - Identify maximum tokens selected in legitimate operations

2. **Test in staging**
   - Deploy with default limits
   - Run full test suite
   - Monitor for limit violations

3. **Adjust if needed**
   - Increase limits only if legitimate operations are blocked
   - Document rationale for any increases

4. **Deploy to production**
   - Roll out gradually (canary → full deployment)
   - Monitor closely for first 24-48 hours
   - Be prepared to adjust limits if needed

### Breaking Changes

This implementation enforces limits by default. Existing deployments must:
- Review and potentially adjust limits before upgrading
- Test thoroughly in non-production environments
- Plan for potential operational impact

## FAQ

**Q: Can I disable these limits?**
A: No. Limits are always enforced for security. You can increase them if needed.

**Q: What happens to locks when selection is aborted?**
A: All acquired locks are automatically released via `UnlockByTxID`.

**Q: Will this affect my existing transactions?**
A: Only if they exceed the default limits. Test in staging first.

**Q: How do I know if limits are too restrictive?**
A: Monitor limit violation metrics and selection success rates.

**Q: Can limits be different per TMS?**
A: Currently no, limits are global. This may be added in future versions.

## References

- [Selector Service Documentation](../services/selector.md)
- [Configuration Guide](../configuration.md)

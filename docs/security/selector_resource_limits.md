# Selector Resource Limits

## Overview

Token selection acquires a short-lived *lock* on each candidate token so that two
concurrent transactions do not try to spend the same token. Under load, a single
wallet can drive a large number of selection/lock requests. To protect the lock store,
to enforce fairness between wallets, or to integrate with an existing quota system,
Panurus offers two ways to throttle this:

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

## The lock function

Both selector drivers route through a `Locker` whose lock function receives the
wallet id, so a custom `Locker` can apply per-wallet policies of its own — per token
lock rather than per selection request.

**Simple selector** — `token/services/selector/simple/selector.go`:

```go
type Locker interface {
    // Lock locks the token id for the consumer transaction txID on behalf of owner
    // (ownerFilter.ID()). Return an error wrapping token.SelectorRateLimited to deny
    // the lock and make the selection fail fast.
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
but do not act on it — they apply no rate limiting or quota.

### Integrating your own rate limiting in a Locker

Provide a `Locker` that wraps the SDK's default locker and enforces your policy before
delegating. Below, a Redis-backed limiter throttles per wallet; the same shape works
for an in-process limiter, a quota table, etc. Note that this is charged **per token lock
attempt**, unlike the built-in limiter above.

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

For the sherdlock selector, provide a `TokenLockStore` (via the
`tokenlockdb.StoreServiceManager` used by `sherdlock.NewService`) whose `Lock` enforces the
limit before delegating to the SQL-backed store.

## Handling the error

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

## Notes

- Passing an empty `walletID` is valid; a `Locker` that keys its policy on wallet id
  should treat empty as "no throttling" (the default lockers ignore it entirely, and so
  does the built-in limiter).
- The built-in limiter is per process. If a wallet's traffic is spread over several nodes,
  each node enforces its own allowance; supply a shared `Limiter` if you need a
  cluster-wide budget.

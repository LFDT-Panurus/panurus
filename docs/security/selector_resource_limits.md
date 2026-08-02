# Selector Resource Limits

## Overview

Token selection acquires a short-lived *lock* on each candidate token so that two
concurrent transactions do not try to spend the same token. Under load, a single
wallet can drive a large number of selection/lock requests.

The Token SDK ships a **built-in, per-wallet rate limiter that is on by default**
and shared by both selector drivers (simple and sherdlock). It is fully
configurable and can be disabled. Applications that need a different policy — for
example a distributed/Redis-backed limiter shared across processes, or a quota
system — can still supply their own `Locker` implementation instead.

Two mechanisms make this work, and both are shared across the two drivers:

1. A **wallet-id-aware lock function**. Both selector drivers pass the wallet id
   the tokens are being selected for into the `Locker`'s lock function, so a
   policy can be applied per wallet.
2. A **fail-fast contract**, `token.SelectorRateLimited`. When a `Locker` denies a
   lock by returning an error that wraps this sentinel, the selector aborts the
   selection immediately and returns the error to the caller instead of retrying.

## The built-in default rate limiter

`token/services/selector/ratelimit` provides a selector-agnostic token-bucket
limiter keyed by wallet id (`ratelimit.TokenBucketRateLimiter`, satisfying the
`ratelimit.Limiter` interface). Each selector wraps its `Locker` with a small
adapter (`simple.NewRateLimitedLocker`, `sherdlock.NewRateLimitedLocker`) that
calls `limiter.Allow(walletID)` before delegating; on denial it returns an error
wrapping `token.SelectorRateLimited`, so the existing fail-fast path handles it.

- **Shared**: both drivers use the same `ratelimit.Limiter` type and the same
  sentinel, so throttling behaves identically regardless of the selected driver.
- **Scope**: one limiter instance per TMS (network/channel/namespace), per
  process, keyed by wallet id.
- **Lifecycle**: the limiter runs a background goroutine to evict idle per-wallet
  buckets; it is stopped automatically when the selector service shuts down.

### Configuration

The limiter is configured under `token.selector` (see
`token/services/selector/config/driver.go`):

```yaml
token:
  selector:
    # Per-wallet token-lock rate (requests/second).
    #   unset / 0  -> default (1000), limiter ENABLED
    #   > 0        -> custom rate, limiter ENABLED
    #   < 0        -> limiter DISABLED
    rateLimit: 1000
    # Per-wallet burst capacity (unset/<=0 -> default 2000).
    rateLimitBurst: 2000
    # How long an idle per-wallet bucket is kept before eviction
    # (unset/<=0 -> default 10m).
    rateLimitIdleTTL: 10m
```

### What the rate counts

The limiter is consulted **once per token-lock attempt**, not once per selection.
A selection locks one candidate token at a time until the requested amount is
covered, and when candidates are already locked by other transactions it retries
the whole scan (`maxImmediateRetries` times per attempt, then `numRetries` times
after a backoff). A single transaction can therefore legitimately issue tens to
hundreds of lock requests, and the amount depends on how the wallet's funds are
split across tokens and on how much concurrency the wallet sees.

The defaults are deliberately generous for this reason: they shed pathological
load (a runaway selection loop) without interfering with normal operation. Pick a
custom `rateLimit` from an observed lock-request rate, not from a target
transaction rate.

To turn the built-in limiter off entirely, set a negative rate:

```yaml
token:
  selector:
    rateLimit: -1
```

## The lock function

Both selector drivers route through a `Locker` whose lock function receives the
wallet id.

**Simple selector** — `token/services/selector/simple/selector.go`:

```go
type Locker interface {
    // Lock locks the token id for the consumer transaction txID on behalf of walletID
    // (ownerFilter.ID()). Return an error wrapping token.SelectorRateLimited to deny
    // the lock and make the selection fail fast.
    Lock(ctx context.Context, id *token.ID, txID string, walletID string, reclaim bool) (string, error)
    UnlockIDs(ctx context.Context, ids ...*token.ID) []*token.ID
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
but do not act on it themselves — the rate limiting is layered on top by the
`ratelimit` adapter described above.

## The fail-fast contract

`token/selector.go` defines:

```go
// SelectorRateLimited is the contract error a Locker implementation returns (directly
// or wrapped) to deny a lock for policy reasons such as rate limiting or quota.
var SelectorRateLimited = errors.New("selection rate limit exceeded")
```

When a `Locker` returns an error `e` with `errors.Is(e, token.SelectorRateLimited)`,
the selector:

- stops iterating candidate tokens,
- releases any tokens it already locked for this request, and
- returns `e` to the caller.

Any *other* error from the lock function keeps the existing semantics: the token is
treated as unavailable (e.g. already locked by another transaction) and selection
continues / retries as before.

## Supplying your own limiter or policy

You can replace the built-in behaviour at two levels.

### Supply a custom `ratelimit.Limiter`

If you only want to swap the throttling algorithm/store but keep the wiring, pass
your own `ratelimit.Limiter` (e.g. Redis-backed) to
`simple.NewRateLimitedLocker` / `sherdlock.NewRateLimitedLocker`:

```go
type Limiter interface {
    // Allow returns nil to permit, or an error wrapping token.SelectorRateLimited to deny.
    Allow(identity string) error
    Stop()
}
```

### Supply a custom `Locker`

For full control (arbitrary policy, quota tables, etc.), disable the built-in
limiter (`token.selector.rateLimit: -1`) and provide your own `Locker` that
enforces the policy before delegating.

#### Simple selector

```go
import (
    "context"

    "github.com/hyperledger-labs/fabric-token-sdk/token"
    "github.com/hyperledger-labs/fabric-token-sdk/token/services/selector/simple"
    tokenapi "github.com/hyperledger-labs/fabric-token-sdk/token/token"
    "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// rateLimitedLocker decorates the default simple.Locker with per-wallet throttling.
type rateLimitedLocker struct {
    simple.Locker            // embeds the SDK's default locker
    limiter       RedisLimiter // your existing infrastructure
}

func (l *rateLimitedLocker) Lock(ctx context.Context, id *tokenapi.ID, txID string, walletID string, reclaim bool) (string, error) {
    if !l.limiter.Allow(ctx, walletID) {
        return "", errors.Wrapf(token.SelectorRateLimited, "wallet %s throttled", walletID)
    }

    return l.Locker.Lock(ctx, id, txID, walletID, reclaim)
}
```

Wire it in by providing a `simple.LockerProvider` whose `New` returns your decorator
instead of the default `inmemory.NewLocker`.

#### Sherdlock selector

Provide a `TokenLockStore` (via the `tokenlockdb.StoreServiceManager` used by
`sherdlock.NewService`) whose `Lock` enforces the limit before delegating to the
SQL-backed store, returning an error wrapping `token.SelectorRateLimited` when a wallet
is throttled.

## Handling the error

Callers should treat `token.SelectorRateLimited` as a transient, retryable-later
condition rather than an insufficient-funds error:

```go
ids, sum, err := selector.Select(ctx, ownerFilter, amount, tokenType)
if errors.Is(err, token.SelectorRateLimited) {
    // back off and retry later, shed the request, or surface a 429-style response
}
```

## Notes

- Passing an empty `walletID` is valid and means "no policy": the built-in limiter
  never throttles an empty wallet id (and does not allocate a bucket for it).
- The built-in limiter's scope is per-TMS, per-process. For fairness across a
  cluster, supply a distributed `ratelimit.Limiter` or a custom `Locker`, whose
  scope, persistence, and lifecycle are entirely under your control.

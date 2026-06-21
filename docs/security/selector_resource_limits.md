# Selector Resource Limits

## Overview

Token selection acquires a short-lived *lock* on each candidate token so that two
concurrent transactions do not try to spend the same token. Under load, a single
wallet can drive a large number of selection/lock requests. Applications that need to
throttle this — to protect the lock store, to enforce fairness between wallets, or to
integrate with an existing quota system — can do so by supplying their own `Locker`
implementation.

Panurus deliberately ships **no built-in rate limiter or quota**. Instead it
gives you two things:

1. A **wallet-id-aware lock function**. Both selector drivers (simple and sherdlock)
   pass the wallet id the tokens are being selected for into the `Locker`'s lock
   function, so a custom `Locker` can apply per-wallet policies.
2. A **fail-fast contract**, `token.SelectorRateLimited`. When a `Locker` denies a
   lock by returning an error that wraps this sentinel, the selector aborts the
   selection immediately and returns the error to the caller instead of retrying.

This keeps the Panurus minimal and lets applications reuse whatever rate-limiting
infrastructure they already run (for example a Redis-backed limiter shared across
processes).

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
but do not act on it — they apply no rate limiting or quota.

## The fail-fast contract

`token/selector.go` defines:

```go
// SelectorRateLimited is the contract error a Locker implementation returns (directly
// or wrapped) to deny a lock for policy reasons such as rate limiting or quota.
var SelectorRateLimited = errors.New("selection rate limit exceeded")
```

When your `Locker` returns an error `e` with `errors.Is(e, token.SelectorRateLimited)`,
the selector:

- stops iterating candidate tokens,
- releases any tokens it already locked for this request, and
- returns `e` to the caller.

Any *other* error from the lock function keeps the existing semantics: the token is
treated as unavailable (e.g. already locked by another transaction) and selection
continues / retries as before.

## Integrating your own rate limiting

Provide a `Locker` that wraps the SDK's default locker and enforces your policy before
delegating. Below, a Redis-backed limiter throttles per wallet; the same shape works
for an in-process limiter, a quota table, etc.

### Simple selector

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

func (l *rateLimitedLocker) Lock(ctx context.Context, id *tokenapi.ID, txID string, walletID string, reclaim bool) (string, error) {
    if !l.limiter.Allow(ctx, walletID) {
        return "", errors.Wrapf(token.SelectorRateLimited, "wallet %s throttled", walletID)
    }

    return l.Locker.Lock(ctx, id, txID, walletID, reclaim)
}
```

Wire it in by providing a `simple.LockerProvider` whose `New` returns your decorator
instead of the default `inmemory.NewLocker`.

### Sherdlock selector

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

- Passing an empty `walletID` is valid; a `Locker` that keys its policy on wallet id
  should treat empty as "no throttling" (the default lockers ignore it entirely).
- Because the policy lives in your `Locker`, its scope (per process vs shared across a
  cluster), persistence, and lifecycle are entirely under your control.

# Selector Resource Limits

## Overview

Token selection acquires a short-lived *lock* on each candidate token so that two
concurrent transactions do not try to spend the same token. Under load, a single wallet can
drive a large number of selection requests, each of which walks and locks candidate tokens.

Panurus does **not** bound that load by default: an unconfigured deployment performs no
selection rate limiting at all. What it ships instead is a **built-in per-wallet rate
limiter that can be switched on with a single configuration key** — for deployments that
want basic protection without writing any code — plus the hooks to tune it or replace it
with your own implementation, for example one backed by Redis and shared across processes.

Three pieces make this up:

1. A **built-in limiter**, `token/services/selector/ratelimit`, off unless it is asked for
   and configured under `token.selector`.
2. A **wallet-id-aware lock function**. Both selector drivers (simple and sherdlock) pass
   the wallet id the tokens are being selected for into the `Locker`'s lock function, so a
   custom `Locker` can apply per-wallet policies of its own.
3. A **fail-fast contract**, `token.SelectorRateLimited`. When a selection is denied by an
   error wrapping this sentinel, the selector aborts immediately and returns the error to
   the caller instead of retrying.

## The built-in limiter

`ratelimit.TokenBucketRateLimiter` is a per-wallet token bucket, **off unless the
configuration switches it on**. Each wallet gets its own bucket, created full on first use
and refilled at `rateLimit` tokens per second up to `rateLimitBurst` tokens, so one wallet's
traffic never consumes another's budget.

Buckets are created lazily, and every `Allow` call reclaims a small, fixed number of buckets
that have gone idle — its own and other wallets' — so memory tracks the set of recently
active wallets rather than growing with every wallet ever seen. Reclamation is amortized
across requests and needs no background goroutine, which is why `Stop` on the built-in
limiter is a no-op. A limiter that stops being used altogether keeps whatever buckets it was
holding, since there is no request left to reclaim them and none being added either.

### What counts as one request

The limiter is charged **once per selection request** — one `Select` call — not once per
token lock. This matters: a single selection locks every candidate token it walks, and both
drivers re-walk the candidate list on internal retries (`sherdlock` up to
`maxImmediateRetries` times per attempt, `simple` up to `numRetries`). Those retries are
driven by contention, not by the caller, so charging per lock attempt would let one
legitimate selection drain its own per-wallet budget.

Metering therefore sits in a `token.SelectorManager` decorator
(`ratelimit.NewSelectorManager`), outside both drivers' retry logic. A denied request is
rejected before the inner selector runs, so it queries no storage and locks no tokens —
there is nothing to unlock on denial.

Unlocking and closing a selector are never throttled, otherwise a throttled wallet could
not release the tokens it already holds.

An **empty wallet id** is never throttled: it carries no wallet to key a per-wallet policy
on, and metering it would let unrelated callers throttle each other.

### Switching it on

The shortest way in — no numbers to choose, the built-in rate and burst apply:

```yaml
token:
  selector:
    rateLimitEnabled: true
```

The full set of keys:

```yaml
token:
  selector:
    # Switch the built-in limiter on with its built-in rate and burst.
    # Omit it (the default) and there is no selection rate limiting at all.
    rateLimitEnabled: true
    # Selections per second per wallet. A positive value switches the limiter
    # on with that rate whether or not rateLimitEnabled is set; a negative
    # value forces it off even when rateLimitEnabled is true. Unset (0) means
    # "use the built-in rate if the limiter is on".
    rateLimit: 100
    # Largest burst of selections a single wallet may perform after an idle
    # period, consulted only when the limiter is on. Unset selects the default
    # (200). Values below rateLimit are raised to rateLimit.
    rateLimitBurst: 200
```

A positive `rateLimit` activates the limiter on its own, so a rate you configure is never
silently ignored; `rateLimitEnabled` exists for the case where you want the protection but
have no opinion about the numbers.

Those built-in numbers are deliberately generous: the limiter is a safety net against a
runaway caller, not a throughput cap on normal traffic. A wallet sustaining more than 100
selections per second is well beyond any realistic interactive workload, while a
saturation test that deliberately drives many concurrent transfers from a *single* wallet
may need `rateLimit` raised.

If throttling is unexpected, the error names the bucket and the limit it exceeded. The bucket
key is the TMS id (`network,channel,namespace`) and the wallet id joined by a NUL byte, shown
here as `␀`:

```
wallet [n1,c1,ns1␀alice] exceeded the selection rate limit of 100 per second (burst 200)
```

## Supplying your own limiter from code

Both selector services take a `WithLimiter` option, which overrides whatever the
configuration selects:

```go
import (
    "github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
    "github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
)

// Install your own limiter.
svc := sherdlock.NewService(fetcherProvider, lockStores, configProvider, metricsProvider,
    sherdlock.WithLimiter(myRedisLimiter))

// Or switch the built-in one on from code, instead of from configuration.
svc = sherdlock.NewService(fetcherProvider, lockStores, configProvider, metricsProvider,
    sherdlock.WithLimiter(ratelimit.NewDefault()))

// Or pin selection rate limiting off, ignoring any configuration.
svc = sherdlock.NewService(fetcherProvider, lockStores, configProvider, metricsProvider,
    sherdlock.WithLimiter(nil))
```

`simple.NewService` takes the same option. A custom limiter implements:

```go
type Limiter interface {
    // Allow returns nil when a selection on behalf of identity may proceed, and an error
    // wrapping token.SelectorRateLimited when it must be throttled.
    Allow(identity string) error
    // Stop is a lifecycle hook for implementations that own background resources, called
    // once the limiter will not be used again. It must be idempotent.
    Stop()
}
```

The selector service only calls `Stop` on a limiter **it created itself** from the
configuration. A limiter installed with `WithLimiter` belongs to the application, and the
application is responsible for stopping it — see [Lifecycle](#lifecycle) for why.

`Allow` is called once per selection, with the wallet id as `identity`, and must be safe
for concurrent use. Denials must wrap `token.SelectorRateLimited` so that callers (and the
selectors) can recognise them:

```go
func (l *redisLimiter) Allow(identity string) error {
    if !l.allow(identity) {
        return errors.Wrapf(token.SelectorRateLimited, "wallet %s throttled", identity)
    }

    return nil
}
```

## Per-wallet policy in a Locker

The limiter above meters whole selection requests. To apply a policy at the level of
individual token locks — a quota on locked tokens, say — enforce it in the `Locker`
instead, which receives the selecting wallet id.

**Simple selector** — `token/services/selector/simple/selector.go`:

```go
type Locker interface {
    // Lock locks the token id for the consumer transaction txID on behalf of the given
    // owner (the wallet the tokens are selected for). Return an error wrapping
    // token.SelectorRateLimited to deny the lock and make the selection fail fast.
    Lock(ctx context.Context, owner string, id *token.ID, txID string, reclaim bool) (string, error)
    UnlockIDs(ctx context.Context, owner string, ids ...*token.ID) []*token.ID
    UnlockByTxID(ctx context.Context, txID string)
    IsLocked(id *token.ID) bool
}
```

Wire it in by providing a `simple.LockerProvider` whose `New` returns your decorator
instead of the default `inmemory.NewLocker`.

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

Provide it through the `tokenlockdb.StoreServiceManager` used by `sherdlock.NewService`.

The built-in in-memory locker and the SQL-backed `TokenLockStore` accept `walletID` but do
not act on it: they apply no rate limiting or quota of their own.

## The fail-fast contract

`token/selector.go` defines:

```go
// SelectorRateLimited is the contract error returned (directly or wrapped) to deny a
// selection for policy reasons such as rate limiting or quota.
var SelectorRateLimited = errors.New("selection rate limit exceeded")
```

When a lock function returns an error `e` with `errors.Is(e, token.SelectorRateLimited)`,
the selector:

- stops iterating candidate tokens,
- releases any tokens it already locked for this request, and
- returns `e` to the caller.

Any *other* error from the lock function keeps the existing semantics: the token is treated
as unavailable (e.g. already locked by another transaction) and selection continues /
retries as before.

## Handling the error

Callers should treat `token.SelectorRateLimited` as a transient, retryable-later condition
rather than an insufficient-funds error:

```go
ids, sum, err := selector.Select(ctx, ownerFilter, amount, tokenType)
if errors.Is(err, token.SelectorRateLimited) {
    // back off and retry later, shed the request, or surface a 429-style response
}
```

## Notes

- Selection rate limiting is **off by default**. A deployment that sets none of the keys
  above and passes no `WithLimiter` option is not throttled at all, and pays nothing for the
  limiter (none is instantiated).
- The built-in limiter is **per process**. A wallet driving selections through several
  nodes gets that budget on each of them; use a shared limiter via `WithLimiter` if you
  need a cluster-wide bound.
- One limiter instance is shared by every TMS a selector service serves, but buckets are
  keyed by the `(TMS id, wallet id)` pair, so identical wallet ids in different TMSes do not
  share a bucket.
- A throttled selection is invisible to the selector's metrics (`SelectionOutcome`,
  `SelectionDuration`), because it never reaches the selector that records them. It is logged
  at debug level by the decorator; otherwise the only signal is the error the caller receives,
  so treat a drop in selection volume after switching the limiter on as expected.

## Lifecycle

`Shutdown` on a selector service is **not only a process-exit hook**:
`ManagementServiceProvider.Update` calls it whenever the public parameters of one of its TMSs
change on the ledger, mid-process.

Because of that, `Shutdown` stops **only a limiter the service built from the configuration**.
A limiter installed with `WithLimiter` is left untouched: stopping it on a routine parameter
update would release resources the application still uses — a shared Redis client, say — while
the selector managers already handed out keep calling `Allow` on it.

So, for a custom limiter:

- Stop it from your own shutdown path, not by relying on the selector service.
- Keep `Allow` working after `Stop` returns, and keep `Stop` idempotent. A limiter that
  hard-fails or starts allowing everything after `Stop` is unsafe here.

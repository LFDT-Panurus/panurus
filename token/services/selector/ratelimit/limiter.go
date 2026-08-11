/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package ratelimit provides the built-in rate limiter for token selection.
//
// Token selection locks every candidate token it walks, so a wallet driving a large
// number of selections puts proportional load on the lock store. The limiter bounds that
// load per wallet.
//
// It is off by default: a deployment that configures nothing is not throttled at all.
// Setting rateLimitEnabled (or a positive rateLimit) under token.selector switches this
// limiter on for deployments that want basic protection without writing any code, and
// applications that already run their own limiter - for example a Redis-backed one shared
// across processes - install it through the selector services' WithLimiter option instead.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// DefaultRate is the number of selection requests per second a single wallet may
	// perform once the limiter is switched on without a rate of its own. It is deliberately
	// generous: the limiter is a safety net against a runaway caller, not a throughput cap
	// on normal traffic, and a wallet sustaining more than this many selections per second
	// is well beyond any realistic interactive workload.
	DefaultRate = 100
	// DefaultBurst is the bucket capacity used when no burst is configured. It absorbs
	// short spikes (a wallet catching up on queued transfers) without raising the
	// sustained rate.
	DefaultBurst = 200
	// DefaultIdleTTL is how long a wallet's bucket is kept after its last request before
	// it becomes eligible for eviction, so that the limiter's memory stays proportional to
	// the set of recently active wallets rather than to all wallets ever seen.
	DefaultIdleTTL = 10 * time.Minute
	// sweepBatch is how many buckets each Allow call inspects for expiry on top of the one
	// it is charging. It keeps reclamation O(1) per request - the alternative, a full scan,
	// would make one request pay for every wallet ever seen - while still bounding the map:
	// Go randomizes the starting point of a map range, so successive calls cover it.
	sweepBatch = 4
)

// Limiter decides whether a token-selection request may proceed.
// Implementations must be safe for concurrent use by multiple goroutines.
type Limiter interface {
	// Allow reports whether a selection request on behalf of identity (the wallet the
	// tokens are selected for) may proceed. It returns nil when the request is allowed,
	// and an error wrapping token.SelectorRateLimited when it must be throttled.
	Allow(identity string) error
	// Stop is a lifecycle hook for implementations that own background resources, called
	// once the limiter will not be used again. It must be idempotent.
	//
	// A selector service only calls Stop on a limiter it created itself from the
	// configuration; a limiter installed with WithLimiter belongs to the application, which
	// owns its lifecycle. That distinction matters because a selector service is shut down
	// whenever the public parameters of one of its TMSs change, not only at process exit.
	Stop()
}

// Config is the subset of the selector configuration the built-in limiter reads.
// *config.Config satisfies it.
type Config interface {
	// GetRateLimit returns the allowed selections per second per wallet.
	// A value of zero or less means rate limiting is disabled.
	GetRateLimit() int
	// GetRateLimitBurst returns the bucket capacity, i.e. the largest burst of
	// selections a single wallet may perform after an idle period.
	GetRateLimitBurst() int
}

// FromConfig returns the built-in limiter configured from cfg, or nil when the
// configuration does not switch rate limiting on - which is the default, so nil is the
// usual answer. A nil Limiter passed to NewSelectorManager leaves the wrapped manager
// unchanged, so callers can hand the result over unconditionally.
func FromConfig(cfg Config) Limiter {
	rate := cfg.GetRateLimit()
	if rate <= 0 {
		return nil
	}

	return NewTokenBucketRateLimiter(rate, cfg.GetRateLimitBurst(), 0)
}

// NewDefault returns the built-in limiter with its built-in rate and burst (DefaultRate,
// DefaultBurst). It is the programmatic equivalent of rateLimitEnabled: true, for
// applications that want basic per-wallet throttling without configuring or writing one:
//
//	svc := sherdlock.NewService(fetcherProvider, lockStores, configProvider, metricsProvider,
//	    sherdlock.WithLimiter(ratelimit.NewDefault()))
func NewDefault() *TokenBucketRateLimiter {
	return NewTokenBucketRateLimiter(DefaultRate, DefaultBurst, 0)
}

// TokenBucketRateLimiter is a per-wallet token bucket. Every wallet gets its own bucket,
// created full on first use and refilled at rate tokens per second up to burst tokens, so
// one wallet's traffic never consumes another's budget. Buckets are created lazily, and
// buckets idle past idleTTL are reclaimed by later Allow calls - each one inspects a small
// batch of them on top of the bucket it charges - so the map tracks the recently active
// wallets instead of growing with every wallet ever seen, and no background goroutine is
// needed. A limiter that stops being used altogether keeps whatever buckets it held: there
// is no call left to reclaim them, and nothing is added either.
type TokenBucketRateLimiter struct {
	// rate is the refill speed in tokens per second. When it is not positive the limiter
	// allows everything.
	rate float64
	// burst is the bucket capacity in tokens.
	burst float64
	// idleTTL is how long a bucket survives without requests.
	idleTTL time.Duration
	// now is the clock, indirected for tests.
	now func() time.Time

	// mu guards buckets, the state of each bucket in it, and now. A single mutex is
	// enough: the critical section is a map lookup and a handful of float operations,
	// orders of magnitude cheaper than the storage round-trips a selection performs.
	mu      sync.Mutex
	buckets map[string]*bucket
}

// bucket is one wallet's token bucket. tokens is the balance as of last.
type bucket struct {
	tokens float64
	last   time.Time
}

// NewTokenBucketRateLimiter returns a limiter allowing rate selections per second per
// wallet with a bucket capacity of burst.
//
// Zero or negative values select sensible substitutes: a non-positive rate yields a
// limiter that allows every request, a burst below rate is raised to rate (a bucket must
// hold at least one second's worth of refill to sustain that rate), and a non-positive
// idleTTL falls back to DefaultIdleTTL.
//
// The limiter starts no background goroutine: idle buckets are reclaimed inside Allow, so
// Stop is a no-op and there is nothing to leak if it is never called.
func NewTokenBucketRateLimiter(rate, burst int, idleTTL time.Duration) *TokenBucketRateLimiter {
	l := &TokenBucketRateLimiter{
		rate:    float64(rate),
		burst:   math.Max(float64(burst), float64(rate)),
		idleTTL: idleTTL,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}

	if l.rate <= 0 {
		// Nothing to meter or evict.
		return l
	}

	if l.idleTTL <= 0 {
		l.idleTTL = DefaultIdleTTL
	}
	// Evicting a bucket resets it to full, which is only free once it would have refilled
	// completely anyway. Keep idle buckets at least that long so eviction can never hand
	// a throttled wallet a fresh budget.
	if refill := time.Duration(l.burst / l.rate * float64(time.Second)); l.idleTTL < refill {
		l.idleTTL = refill
	}

	return l
}

// Allow charges one token to identity's bucket. It returns an error wrapping
// token.SelectorRateLimited when the bucket is empty, which makes both selectors abort the
// selection immediately instead of retrying.
func (l *TokenBucketRateLimiter) Allow(identity string) error {
	// No rate configured: nothing is throttled.
	if l.rate <= 0 {
		return nil
	}
	// An empty identity carries no wallet to key a per-wallet policy on. Treating it as a
	// single shared bucket would let unrelated callers throttle each other, so it is not
	// metered at all - the same "empty means no policy" rule the Locker contract states.
	if identity == "" {
		return nil
	}

	if !l.take(identity) {
		return errors.Wrapf(
			token.SelectorRateLimited,
			"wallet [%s] exceeded the selection rate limit of %g per second (burst %g)",
			identity, l.rate, l.burst,
		)
	}

	return nil
}

// take refills identity's bucket for the elapsed time and consumes one token from it,
// reporting whether a token was available. Buckets idle past idleTTL are evicted here
// and re-created full: the idleTTL invariant guarantees they would have fully refilled
// anyway, so no accounting is lost and no background goroutine is needed.
func (l *TokenBucketRateLimiter) take(identity string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	// Reclaim a bounded number of other wallets' expired buckets. Without this, only the
	// wallet currently being charged is ever cleaned up, and a wallet that stops selecting
	// keeps its bucket for the lifetime of the process.
	l.sweep(now, identity)

	b, ok := l.buckets[identity]
	if !ok || now.Sub(b.last) > l.idleTTL {
		// A wallet not seen recently (or one returning after a long idle) starts with a
		// full bucket. The idleTTL >= refill-time invariant guarantees the bucket would
		// have been full by now anyway, so re-creating it is safe.
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[identity] = b
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed.Seconds()*l.rate)
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--

	return true
}

// sweep evicts up to sweepBatch expired buckets, skipping charged (which take is about to
// refresh anyway). It inspects a bounded slice of the map rather than all of it, so the
// cost per request does not grow with the number of wallets the limiter has seen; Go's
// randomized map range start is what makes successive calls reach every bucket eventually.
//
// Callers must hold l.mu. Deleting from a map that is being ranged over is defined
// behaviour in Go: an entry deleted before it is reached is simply not produced.
func (l *TokenBucketRateLimiter) sweep(now time.Time, charged string) {
	inspected := 0
	for id, b := range l.buckets {
		if inspected >= sweepBatch {
			return
		}
		inspected++

		if id != charged && now.Sub(b.last) > l.idleTTL {
			delete(l.buckets, id)
		}
	}
}

// Stop is a no-op: the limiter owns no background goroutine, and idle buckets are reclaimed
// by Allow. It satisfies the Limiter lifecycle hook, is idempotent, and Allow keeps working
// after it - so a selector service shutting down on a public-parameters update cannot break
// a limiter that is still in use.
func (l *TokenBucketRateLimiter) Stop() {}

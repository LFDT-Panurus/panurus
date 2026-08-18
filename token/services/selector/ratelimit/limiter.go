/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package ratelimit provides an opt-in, built-in per-wallet rate limiter for token selection.
//
// The limiter is disabled by default. It is activated either from configuration
// (the token.selector.rateLimit* keys, see token/services/selector/config) or programmatically
// through the functional options in this package (WithLimiter, WithDefaultLimiter).
//
// Metering happens once per selection request, that is once per Selector.Select call, not once
// per token lock attempt. Decorate wires this up by wrapping a token.SelectorManager. Charging
// per lock attempt would let the internal contention retries of the selectors drain a wallet's
// allowance, and would charge a large selection more than a small one.
//
// When a wallet exceeds its allowance, the limiter returns an error wrapping
// token.SelectorRateLimited, the fail-fast contract both selector drivers already honour: the
// selection aborts immediately, no tokens stay locked, and the error reaches the caller.
package ratelimit

import (
	"context"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// DefaultRate is the per-wallet selection rate, in requests per second, used when rate
	// limiting is enabled without an explicit rate. It is high enough not to interfere with
	// normal interactive workloads while still capping a runaway or abusive client.
	DefaultRate = 100.0
	// DefaultBurstFactor multiplies the rate to obtain the default burst capacity, so the
	// default configuration allows DefaultRate*DefaultBurstFactor back-to-back requests.
	DefaultBurstFactor = 2
	// DefaultIdleTimeout is how long a wallet bucket may stay untouched before it becomes
	// eligible for eviction.
	DefaultIdleTimeout = 5 * time.Minute
	// DefaultMaxBuckets caps the number of live wallet buckets, bounding the memory a
	// long-running node spends on transient wallet ids.
	DefaultMaxBuckets = 4096

	// maxIdleTimeout caps the idle timeout derived from a very slow rate (see normalize).
	maxIdleTimeout = time.Hour
	// sweepEveryNCalls is the number of Allow calls between two amortized sweeps of idle
	// buckets. Sweeping is O(number of buckets), so it must not run on every call.
	sweepEveryNCalls = 512
	// evictionWarnInterval is the minimum time between two warnings about a limiter that sits
	// at its bucket cap. At the cap every new wallet evicts one, so warning every time would
	// flood the log.
	evictionWarnInterval = time.Minute
	// keySeparator joins the scope and the wallet id. It cannot appear in either, so two
	// distinct (scope, wallet) pairs can never map to the same bucket.
	keySeparator = "\x00"
)

var logger = logging.MustGetLogger()

// Limiter meters token selection requests per wallet.
//
// Implementations must be safe for concurrent use.
type Limiter interface {
	// Allow reports whether a selection request for walletID within scope may proceed.
	// scope isolates wallets belonging to different token management services, so that the
	// same wallet id used in two networks or namespaces does not share one allowance.
	// It returns nil when the request is allowed, and an error wrapping
	// token.SelectorRateLimited when the request must be denied.
	// An empty walletID is never throttled.
	Allow(ctx context.Context, scope string, walletID string) error
}

// Config configures the built-in token-bucket limiter. Non-positive fields fall back to the
// package defaults, so the zero Config is the default configuration.
type Config struct {
	// Rate is the sustained number of selection requests allowed per second, per wallet.
	Rate float64
	// Burst is the maximum number of selection requests a single wallet may issue
	// back-to-back before it is throttled down to Rate.
	Burst int
	// IdleTimeout is how long a bucket may stay untouched before it becomes eligible for
	// eviction. It is raised to at least the time a bucket needs to refill from empty.
	IdleTimeout time.Duration
	// MaxBuckets is the maximum number of live buckets. Once the idle sweep cannot bring the
	// limiter back under this number, the least recently used buckets are dropped.
	MaxBuckets int
}

// DefaultConfig returns the configuration used when rate limiting is enabled without any
// explicit rate or burst: DefaultRate requests per second per wallet, with a burst of
// DefaultRate*DefaultBurstFactor.
func DefaultConfig() Config {
	return Config{
		Rate:        DefaultRate,
		Burst:       DefaultRate * DefaultBurstFactor,
		IdleTimeout: DefaultIdleTimeout,
		MaxBuckets:  DefaultMaxBuckets,
	}
}

// normalize replaces non-positive fields with their defaults and raises IdleTimeout to at least
// the time a bucket needs to refill from empty to full. Evicting an idle bucket hands back a
// full bucket, so a shorter idle timeout would let a wallet skip part of the wait it owes simply
// by pausing.
func (c Config) normalize() Config {
	if c.Rate <= 0 {
		c.Rate = DefaultRate
	}
	if c.Burst <= 0 {
		c.Burst = max(int(math.Ceil(c.Rate*DefaultBurstFactor)), 1)
	}
	if c.MaxBuckets <= 0 {
		c.MaxBuckets = DefaultMaxBuckets
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if refill := refillDuration(float64(c.Burst), c.Rate); c.IdleTimeout < refill {
		c.IdleTimeout = refill
	}

	return c
}

// refillDuration returns how long it takes to accumulate the given number of requests at rate
// requests per second, capped at maxIdleTimeout to keep the result a sane duration.
func refillDuration(requests float64, rate float64) time.Duration {
	seconds := requests / rate
	if seconds >= maxIdleTimeout.Seconds() {
		return maxIdleTimeout
	}

	return time.Duration(seconds * float64(time.Second))
}

// bucket is the per-wallet token bucket. It is only ever accessed with BucketLimiter.mu held.
type bucket struct {
	// tokens is the number of selection requests currently available to the wallet.
	tokens float64
	// last is the time tokens was last recomputed, and doubles as the wallet's last-seen
	// time for eviction purposes.
	last time.Time
}

// refill adds the requests accrued since the last access, capping at burst.
func (b *bucket) refill(now time.Time, rate float64, burst float64) {
	elapsed := now.Sub(b.last)
	if elapsed > 0 {
		b.tokens = math.Min(burst, b.tokens+elapsed.Seconds()*rate)
	}
	b.last = now
}

// BucketLimiter is a thread-safe token-bucket Limiter keyed by (scope, wallet id).
//
// Buckets are allocated lazily on first use and pruned again without a background goroutine:
// every sweepEveryNCalls requests, and whenever the bucket count exceeds MaxBuckets, idle
// buckets are removed. This keeps the memory of a long-running node bounded even when it sees
// a large number of short-lived wallet ids.
type BucketLimiter struct {
	rate        float64
	burst       float64
	idleTimeout time.Duration
	maxBuckets  int
	// now returns the current time. It is a field so tests can drive the bucket with a
	// deterministic clock instead of sleeping.
	now func() time.Time

	mu               sync.Mutex
	buckets          map[string]*bucket
	callsSinceSweep  int
	lastEvictionWarn time.Time
}

// New returns a BucketLimiter with the given configuration. Non-positive configuration values
// fall back to the package defaults, so New(Config{}) is the default limiter.
func New(c Config) *BucketLimiter {
	c = c.normalize()

	return &BucketLimiter{
		rate:        c.Rate,
		burst:       float64(c.Burst),
		idleTimeout: c.IdleTimeout,
		maxBuckets:  c.MaxBuckets,
		now:         time.Now,
		buckets:     make(map[string]*bucket),
	}
}

// Allow consumes one request from the bucket of walletID within scope. It returns an error
// wrapping token.SelectorRateLimited when the wallet has no request left, and nil otherwise.
// An empty walletID is never throttled.
func (l *BucketLimiter) Allow(_ context.Context, scope string, walletID string) error {
	if len(walletID) == 0 {
		return nil
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.maybeSweep(now)

	key := bucketKey(scope, walletID)
	b, ok := l.buckets[key]
	if !ok {
		l.makeRoom(now)
		// A new wallet starts with a full bucket, which is also what an evicted wallet gets
		// back: see Config.normalize for why that is safe.
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.refill(now, l.rate, l.burst)
	}

	if b.tokens < 1 {
		retryAfter := refillDuration(1-b.tokens, l.rate)

		return errors.Wrapf(
			token.SelectorRateLimited,
			"wallet [%s] exceeded the selection rate of %g requests/s (burst %g) for tms [%s], retry in %s",
			walletID, l.rate, l.burst, scope, retryAfter,
		)
	}
	b.tokens--

	return nil
}

// Stop releases the memory held by the limiter. The limiter stays usable afterwards, with every
// wallet starting from a full bucket again.
//
// A selector service never calls this: its Shutdown also runs on routine public-parameter
// reloads, and resetting every wallet's allowance there would let a throttled client wash out
// its debt. Stop is for callers that own a limiter and are done with it.
func (l *BucketLimiter) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.buckets = make(map[string]*bucket)
	l.callsSinceSweep = 0
}

// BucketCount returns the number of live buckets. It is exported for tests and diagnostics.
func (l *BucketLimiter) BucketCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.buckets)
}

// maybeSweep prunes idle buckets once every sweepEveryNCalls calls. Sweeping is linear in the
// number of buckets, so spreading it over many calls keeps Allow constant-time on average.
// It must be called with l.mu held.
func (l *BucketLimiter) maybeSweep(now time.Time) {
	l.callsSinceSweep++
	if l.callsSinceSweep < sweepEveryNCalls {
		return
	}
	l.callsSinceSweep = 0
	l.sweepIdle(now)
}

// makeRoom guarantees there is room for one more bucket, so the limiter never holds more than
// maxBuckets of them. It first tries the cheap route of dropping idle buckets, and only evicts
// live ones if that was not enough. It must be called with l.mu held.
func (l *BucketLimiter) makeRoom(now time.Time) {
	if len(l.buckets) < l.maxBuckets {
		return
	}

	l.sweepIdle(now)
	if excess := len(l.buckets) - l.maxBuckets + 1; excess > 0 {
		l.evictLeastRecentlyUsed(now, excess)
	}
}

// sweepIdle removes every bucket that has not been used for idleTimeout. It must be called with
// l.mu held.
func (l *BucketLimiter) sweepIdle(now time.Time) {
	for key, b := range l.buckets {
		if now.Sub(b.last) >= l.idleTimeout {
			delete(l.buckets, key)
		}
	}
}

// evictLeastRecentlyUsed drops the n buckets that were accessed longest ago. It runs only when
// pruning idle buckets was not enough to stay within maxBuckets, which means the node is tracking
// more active wallets than it is configured for: bounding memory takes precedence over remembering
// every wallet's debt. It must be called with l.mu held.
func (l *BucketLimiter) evictLeastRecentlyUsed(now time.Time, n int) {
	// At the cap, every new wallet evicts one, so this is throttled to keep the log readable.
	if now.Sub(l.lastEvictionWarn) >= evictionWarnInterval {
		l.lastEvictionWarn = now
		logger.Warnf(
			"selection rate limiter is at its maximum of %d buckets and is evicting the least recently used "+
				"wallets, which resets their allowance: consider raising token.selector.rateLimitMaxBuckets",
			l.maxBuckets,
		)
	} else {
		logger.Debugf("selection rate limiter evicting %d least recently used buckets of %d", n, len(l.buckets))
	}

	keys := make([]string, 0, len(l.buckets))
	for key := range l.buckets {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return l.buckets[a].last.Compare(l.buckets[b].last)
	})
	for _, key := range keys[:n] {
		delete(l.buckets, key)
	}
}

// bucketKey scopes a wallet id to its token management service, so the same wallet id in two
// networks or namespaces does not share an allowance.
func bucketKey(scope string, walletID string) string {
	return scope + keySeparator + walletID
}

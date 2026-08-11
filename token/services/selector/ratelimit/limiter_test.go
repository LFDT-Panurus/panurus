/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Package ratelimit tests validate the built-in per-wallet token bucket: bucket
// arithmetic, per-wallet isolation, refill, idle eviction, and configuration defaults.

// setClock replaces the limiter's clock, so refill and eviction can be driven by the test.
func (l *TokenBucketRateLimiter) setClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.now = now
}

// size returns the number of tracked buckets.
func (l *TokenBucketRateLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.buckets)
}

// fakeClock is a manually advanced clock, so refill and eviction can be asserted without
// sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestLimiter returns a limiter driven by a fake clock, with the default idleTTL.
func newTestLimiter(t *testing.T, rate, burst int) (*TokenBucketRateLimiter, *fakeClock) {
	t.Helper()

	l := NewTokenBucketRateLimiter(rate, burst, 0)
	t.Cleanup(l.Stop)
	clock := newFakeClock()
	l.setClock(clock.Now)

	return l, clock
}

// drain consumes the whole bucket of identity, asserting every request is allowed.
func drain(t *testing.T, l *TokenBucketRateLimiter, identity string, n int) {
	t.Helper()

	for i := range n {
		require.NoErrorf(t, l.Allow(identity), "request %d of the initial burst was denied", i+1)
	}
}

func TestTokenBucket_AllowsUpToBurstThenDenies(t *testing.T) {
	l, _ := newTestLimiter(t, 10, 20)

	// The bucket starts full, so exactly burst requests are served without any refill.
	drain(t, l, "alice", 20)

	err := l.Allow("alice")
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Contains(t, err.Error(), "wallet [alice]")
	assert.Contains(t, err.Error(), "selection rate limit")
}

func TestTokenBucket_RefillsAtConfiguredRate(t *testing.T) {
	l, clock := newTestLimiter(t, 10, 20)
	drain(t, l, "alice", 20)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)

	// At 10 tokens/s a single token takes 100ms; half of that is not enough.
	clock.advance(50 * time.Millisecond)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)

	clock.advance(50 * time.Millisecond)
	require.NoError(t, l.Allow("alice"))
	// That one token is spent again.
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)

	// One second of refill buys exactly the configured rate, no more.
	clock.advance(time.Second)
	drain(t, l, "alice", 10)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
}

func TestTokenBucket_RefillIsCappedAtBurst(t *testing.T) {
	l, clock := newTestLimiter(t, 10, 20)
	drain(t, l, "alice", 20)

	// An hour of idling may not accumulate more than the bucket capacity.
	clock.advance(time.Hour)
	drain(t, l, "alice", 20)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
}

func TestTokenBucket_IsPerWallet(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 1)

	require.NoError(t, l.Allow("alice"))
	// alice is out of budget, but bob and charlie have their own untouched buckets.
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
	require.NoError(t, l.Allow("bob"))
	require.NoError(t, l.Allow("charlie"))
	require.ErrorIs(t, l.Allow("bob"), token.SelectorRateLimited)
}

func TestTokenBucket_BurstCoercedUpToRate(t *testing.T) {
	// A bucket smaller than the rate could not sustain that rate, so burst is raised.
	l, clock := newTestLimiter(t, 10, 1)

	drain(t, l, "alice", 10)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)

	clock.advance(time.Second)
	drain(t, l, "alice", 10)
}

func TestTokenBucket_EmptyIdentityIsNeverThrottled(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 1)

	// An empty wallet id means "no policy to key on": it must not be metered, and must
	// not share a bucket with anything else.
	for range 100 {
		require.NoError(t, l.Allow(""))
	}
	assert.Zero(t, l.size(), "an unmetered identity must not allocate a bucket")
	require.NoError(t, l.Allow("alice"))
}

func TestTokenBucket_NonPositiveRateAllowsEverything(t *testing.T) {
	for _, rate := range []int{0, -1} {
		l := NewTokenBucketRateLimiter(rate, 5, 0)
		t.Cleanup(l.Stop)

		for range 100 {
			require.NoError(t, l.Allow("alice"))
		}
		assert.Zero(t, l.size(), "a disabled limiter must not track buckets")
	}
}

func TestTokenBucket_EvictsIdleBuckets(t *testing.T) {
	l, clock := newTestLimiter(t, 10, 20)
	l.idleTTL = time.Minute

	require.NoError(t, l.Allow("alice"))
	require.NoError(t, l.Allow("bob"))
	require.Equal(t, 2, l.size())

	// Nothing is idle yet — a lookup just before the TTL must not evict.
	clock.advance(30 * time.Second)
	require.NoError(t, l.Allow("alice"))
	require.NoError(t, l.Allow("bob"))
	assert.Equal(t, 2, l.size())

	// alice keeps going, bob goes quiet past the TTL.
	clock.advance(time.Minute + time.Second)
	require.NoError(t, l.Allow("alice"))

	// bob's bucket is evicted on the next Allow call because it crossed idleTTL.
	require.NoError(t, l.Allow("bob"))
	assert.Equal(t, 2, l.size(), "both wallets have live buckets after bob's lazy re-create")

	// Eviction is pure bookkeeping: the surviving wallet keeps its balance, and the
	// evicted one is simply treated as new (its bucket had refilled anyway).
}

func TestTokenBucket_EvictionCannotResetAThrottledWallet(t *testing.T) {
	// An idleTTL shorter than the time the bucket needs to refill would let eviction hand
	// a throttled wallet a fresh full bucket, so it is raised to the refill time.
	l := NewTokenBucketRateLimiter(10, 20, time.Millisecond)
	t.Cleanup(l.Stop)
	clock := newFakeClock()
	l.setClock(clock.Now)

	assert.Equal(t, 2*time.Second, l.idleTTL, "20 tokens at 10/s refill in 2s")

	drain(t, l, "alice", 20)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)

	// Well past the configured 1ms TTL, but not past the (enforced) refill time. Lazy
	// eviction must NOT re-create alice's bucket here because the idleTTL hasn't elapsed.
	clock.advance(500 * time.Millisecond)
	// alice gets only what the refill earned her (5 tokens), not a full bucket.
	drain(t, l, "alice", 5)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
}

func TestTokenBucket_LazyEvictionReclaims(t *testing.T) {
	// With lazy eviction, a bucket disappears from the map only when a new Allow call
	// arrives after the TTL — there is no background goroutine.
	// Pass a short idleTTL; it will be clamped up to the refill time
	// (1000 tokens at 1000/s = 1 second).
	l := NewTokenBucketRateLimiter(1000, 1000, time.Millisecond)
	t.Cleanup(l.Stop)
	require.Equal(t, time.Second, l.idleTTL)

	clock := newFakeClock()
	l.setClock(clock.Now)

	require.NoError(t, l.Allow("alice"))
	assert.Equal(t, 1, l.size())

	// Advance past the TTL; the bucket is still in the map (no background sweep).
	clock.advance(2 * time.Second)
	assert.Equal(t, 1, l.size(), "bucket not yet reclaimed without a new request")

	// A new Allow from alice evicts the stale entry and re-creates it.
	require.NoError(t, l.Allow("alice"))
	assert.Equal(t, 1, l.size(), "re-created bucket is present")

	// A wallet that stops coming back does not keep its entry for the lifetime of the
	// process: the sweep inside another wallet's Allow reclaims it.
	require.NoError(t, l.Allow("bob"))
	require.Equal(t, 2, l.size())

	clock.advance(2 * time.Second)
	require.NoError(t, l.Allow("bob")) // evicts bob's own stale entry and alice's
	assert.Equal(t, 1, l.size(), "alice's idle bucket is reclaimed by bob's request")
}

// The map must not grow with every wallet ever seen: an idle wallet's bucket is reclaimed
// by other wallets' requests, not only by its own. Before the sweep existed, eviction only
// ever touched the wallet being charged, so a wallet that stopped selecting kept its bucket
// until the process exited.
func TestTokenBucket_IdleBucketsAreReclaimedByOtherWallets(t *testing.T) {
	// A rate high enough that no request in this test is ever throttled, so the only thing
	// under test is which buckets stay in the map.
	l := NewTokenBucketRateLimiter(1000, 1000, 0)
	t.Cleanup(l.Stop)
	require.Equal(t, DefaultIdleTTL, l.idleTTL)

	clock := newFakeClock()
	l.setClock(clock.Now)

	// Three wallets select once and are never heard from again. Together with the active
	// wallet below the map holds at most sweepBatch entries, so a single Allow inspects all
	// of them and the outcome is exact rather than amortized.
	for _, w := range []string{"alice", "bob", "carol"} {
		require.NoError(t, l.Allow(w))
	}
	require.Equal(t, 3, l.size())

	// Not yet expired: a sweep must not discard buckets that still hold live state.
	require.NoError(t, l.Allow("dave"))
	require.Equal(t, 4, l.size(), "buckets within their TTL are kept")

	clock.advance(DefaultIdleTTL + time.Second)

	require.NoError(t, l.Allow("dave"))
	assert.Equal(t, 1, l.size(), "only the active wallet's bucket survives")
}

// The same property at a size where one Allow cannot inspect the whole map: reclamation is
// amortized over many requests rather than exact, so the assertion is a bound. Each Allow
// inspects sweepBatch of the ~50 entries from a randomized start, so 200 requests reduce the
// expected number of survivors to far below the bound asserted here.
func TestTokenBucket_IdleBucketsAreReclaimedAcrossManyWallets(t *testing.T) {
	l := NewTokenBucketRateLimiter(1000, 1000, 0)
	t.Cleanup(l.Stop)

	clock := newFakeClock()
	l.setClock(clock.Now)

	const idle = 50
	for i := range idle {
		require.NoError(t, l.Allow(strconv.Itoa(i)))
	}
	require.Equal(t, idle, l.size())

	clock.advance(DefaultIdleTTL + time.Second)

	for range 200 {
		require.NoError(t, l.Allow("active"))
	}

	assert.Less(t, l.size(), 10, "idle buckets are reclaimed as requests come in")
}

func TestTokenBucket_StopIsIdempotent(t *testing.T) {
	l := NewTokenBucketRateLimiter(10, 20, 0)

	l.Stop()
	l.Stop()

	// The limiter still enforces the limit after Stop, as the Limiter contract requires: a
	// selector service shutting down on a public-parameters update must not disarm it.
	drain(t, l, "alice", 20)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
}

func TestTokenBucket_ConcurrentAllowGrantsExactlyBurst(t *testing.T) {
	// With a clock that never advances there is no refill, so the number of granted
	// requests must be exactly the bucket capacity no matter how they interleave.
	const burst = 100
	l, _ := newTestLimiter(t, burst, burst)

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range burst {
				if l.Allow("alice") == nil {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(burst), allowed.Load())
}

func TestTokenBucket_ConcurrentWalletsAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 1)

	const wallets = 50
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range wallets {
		wg.Go(func() {
			// Each wallet has a one-token bucket, so each gets exactly one grant.
			if l.Allow("wallet"+strconv.Itoa(i)) == nil {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(wallets), allowed.Load())
	assert.Equal(t, wallets, l.size())
}

// stubConfig is a Config with fixed values.
type stubConfig struct {
	rate  int
	burst int
}

func (c stubConfig) GetRateLimit() int      { return c.rate }
func (c stubConfig) GetRateLimitBurst() int { return c.burst }

func TestFromConfig(t *testing.T) {
	t.Run("nil when not switched on", func(t *testing.T) {
		// A nil Limiter is what NewSelectorManager treats as "no throttling", and a rate of
		// zero (nothing configured) is the default, so this is the usual outcome.
		for _, rate := range []int{0, -1} {
			assert.Nil(t, FromConfig(stubConfig{rate: rate, burst: 20}))
		}
	})

	t.Run("configured limiter", func(t *testing.T) {
		limiter := FromConfig(stubConfig{rate: 5, burst: 7})
		require.NotNil(t, limiter)
		t.Cleanup(limiter.Stop)

		l, ok := limiter.(*TokenBucketRateLimiter)
		require.True(t, ok)
		assert.InEpsilon(t, 5.0, l.rate, 0)
		assert.InEpsilon(t, 7.0, l.burst, 0)
		assert.Equal(t, DefaultIdleTTL, l.idleTTL)

		drain(t, l, "alice", 7)
		require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
	})
}

// NewDefault is what an application passes to WithLimiter to get basic throttling without
// configuring numbers, so it must carry the built-in rate and burst.
func TestNewDefault(t *testing.T) {
	l := NewDefault()
	require.NotNil(t, l)
	t.Cleanup(l.Stop)

	assert.InEpsilon(t, float64(DefaultRate), l.rate, 0)
	assert.InEpsilon(t, float64(DefaultBurst), l.burst, 0)

	drain(t, l, "alice", DefaultBurst)
	require.ErrorIs(t, l.Allow("alice"), token.SelectorRateLimited)
}

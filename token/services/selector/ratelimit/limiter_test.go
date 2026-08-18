/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testScope = "testnet,testchannel,testns"

// fakeClock is a manually advanced clock, so the bucket arithmetic can be tested without sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// newTestLimiter returns a limiter driven by the returned fake clock.
func newTestLimiter(t *testing.T, c Config) (*BucketLimiter, *fakeClock) {
	t.Helper()

	clock := newFakeClock()
	l := New(c)
	l.now = clock.Now

	return l, clock
}

// TestBucketLimiter_BurstThenDeny verifies a wallet may issue exactly Burst requests back-to-back
// and is denied afterwards, with an error wrapping token.SelectorRateLimited.
func TestBucketLimiter_BurstThenDeny(t *testing.T) {
	l, _ := newTestLimiter(t, Config{Rate: 10, Burst: 3})
	ctx := context.Background()

	for i := range 3 {
		require.NoError(t, l.Allow(ctx, testScope, "alice"), "request %d must be allowed", i)
	}

	err := l.Allow(ctx, testScope, "alice")
	require.Error(t, err)
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Contains(t, err.Error(), "alice")
	assert.Contains(t, err.Error(), testScope)
}

// TestBucketLimiter_Refill verifies the bucket refills at the configured rate and never beyond
// the burst capacity.
func TestBucketLimiter_Refill(t *testing.T) {
	// 10 requests/s means one request every 100ms.
	l, clock := newTestLimiter(t, Config{Rate: 10, Burst: 2})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)

	// Not enough time for a whole request yet.
	clock.Advance(50 * time.Millisecond)
	require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)

	// A further 50ms completes the first refilled request.
	clock.Advance(50 * time.Millisecond)
	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)

	// A long pause refills at most Burst requests, not more.
	clock.Advance(time.Hour)
	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)
}

// TestBucketLimiter_SustainedRate verifies that, once the burst is spent, a wallet is served at
// the configured sustained rate.
func TestBucketLimiter_SustainedRate(t *testing.T) {
	l, clock := newTestLimiter(t, Config{Rate: 4, Burst: 1})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	for range 10 {
		require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)
		clock.Advance(250 * time.Millisecond) // exactly one request at 4 requests/s
		require.NoError(t, l.Allow(ctx, testScope, "alice"))
	}
}

// TestBucketLimiter_EmptyWalletBypass verifies an empty wallet id is never throttled and does not
// allocate a bucket.
func TestBucketLimiter_EmptyWalletBypass(t *testing.T) {
	l, _ := newTestLimiter(t, Config{Rate: 1, Burst: 1})
	ctx := context.Background()

	for range 100 {
		require.NoError(t, l.Allow(ctx, testScope, ""))
	}
	assert.Equal(t, 0, l.BucketCount())
}

// TestBucketLimiter_WalletIsolation verifies one wallet exhausting its allowance does not affect
// another.
func TestBucketLimiter_WalletIsolation(t *testing.T) {
	l, _ := newTestLimiter(t, Config{Rate: 1, Burst: 1})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)

	require.NoError(t, l.Allow(ctx, testScope, "bob"))
	require.ErrorIs(t, l.Allow(ctx, testScope, "bob"), token.SelectorRateLimited)
}

// TestBucketLimiter_ScopeIsolation verifies the same wallet id in two token management services
// gets two independent allowances, and that scope and wallet id cannot be confused for one
// another.
func TestBucketLimiter_ScopeIsolation(t *testing.T) {
	l, _ := newTestLimiter(t, Config{Rate: 1, Burst: 1})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, "net1,ch1,ns1", "alice"))
	require.ErrorIs(t, l.Allow(ctx, "net1,ch1,ns1", "alice"), token.SelectorRateLimited)

	require.NoError(t, l.Allow(ctx, "net2,ch1,ns1", "alice"))
	require.Equal(t, 2, l.BucketCount())

	// ("a", "bc") and ("ab", "c") must not collide.
	require.NoError(t, l.Allow(ctx, "a", "bc"))
	require.NoError(t, l.Allow(ctx, "ab", "c"))
	require.Equal(t, 4, l.BucketCount())
}

// TestBucketLimiter_ConcurrentWallets verifies that under concurrency each wallet gets exactly its
// own allowance: no cross-wallet interference, and no double spending of a bucket.
func TestBucketLimiter_ConcurrentWallets(t *testing.T) {
	const (
		wallets            = 16
		goroutinesPerWalet = 8
		requestsPerRoutine = 25
		burst              = 40
	)

	// Rate 0 would be replaced by the default, so use a rate slow enough that the fake clock,
	// which never advances, cannot refill anything.
	l, _ := newTestLimiter(t, Config{Rate: 0.0001, Burst: burst})
	ctx := context.Background()

	allowed := make([]atomic.Int64, wallets)
	var wg sync.WaitGroup
	for w := range wallets {
		walletID := "wallet-" + strconv.Itoa(w)
		for range goroutinesPerWalet {
			wg.Go(func() {
				for range requestsPerRoutine {
					if err := l.Allow(ctx, testScope, walletID); err == nil {
						allowed[w].Add(1)
					} else {
						assert.ErrorIs(t, err, token.SelectorRateLimited)
					}
				}
			})
		}
	}
	wg.Wait()

	// Every wallet asks for more than its burst, so each must be served exactly burst times.
	require.Greater(t, goroutinesPerWalet*requestsPerRoutine, burst, "the test must ask for more than the burst")
	for w := range wallets {
		assert.Equal(t, int64(burst), allowed[w].Load(), "wallet %d", w)
	}
	assert.Equal(t, wallets, l.BucketCount())
}

// TestBucketLimiter_IdleEviction verifies idle buckets are pruned during ordinary access, so a
// node that sees many short-lived wallet ids does not accumulate them.
func TestBucketLimiter_IdleEviction(t *testing.T) {
	l, clock := newTestLimiter(t, Config{Rate: 100, Burst: 100, IdleTimeout: time.Minute})
	ctx := context.Background()

	// A batch of one-shot wallets, none of which is idle yet, so all of them are kept.
	for i := range sweepEveryNCalls {
		require.NoError(t, l.Allow(ctx, testScope, "transient-"+strconv.Itoa(i)))
	}
	assert.Equal(t, sweepEveryNCalls, l.BucketCount())

	// They all go idle, and the sweep that follows within the next sweepEveryNCalls requests
	// of an active wallet removes them, leaving only that wallet's bucket behind.
	clock.Advance(2 * time.Minute)
	for range sweepEveryNCalls {
		// The active wallet is throttled along the way, which is beside the point here.
		_ = l.Allow(ctx, testScope, "alice")
	}
	assert.Equal(t, 1, l.BucketCount())
}

// TestBucketLimiter_BoundedUnderWalletTurnover verifies the bucket count stays bounded when a
// long-running node keeps seeing new wallet ids without any of them going idle.
func TestBucketLimiter_BoundedUnderWalletTurnover(t *testing.T) {
	const maxBuckets = 64

	l, clock := newTestLimiter(t, Config{Rate: 100, Burst: 100, IdleTimeout: time.Hour, MaxBuckets: maxBuckets})
	ctx := context.Background()

	for i := range 20 * maxBuckets {
		require.NoError(t, l.Allow(ctx, testScope, "wallet-"+strconv.Itoa(i)))
		// No bucket ever reaches the idle timeout, so only the hard cap bounds memory.
		clock.Advance(time.Millisecond)
		require.LessOrEqual(t, l.BucketCount(), maxBuckets, "bucket count must stay bounded")
	}
}

// TestBucketLimiter_EvictsLeastRecentlyUsed verifies that, when the idle sweep is not enough, it
// is the least recently used buckets that go.
func TestBucketLimiter_EvictsLeastRecentlyUsed(t *testing.T) {
	// A rate slow enough that the seconds the clock advances refill nothing measurable.
	l, clock := newTestLimiter(t, Config{Rate: 0.001, Burst: 1, IdleTimeout: time.Hour, MaxBuckets: 2})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, testScope, "old"))
	clock.Advance(time.Second)
	require.NoError(t, l.Allow(ctx, testScope, "recent"))
	clock.Advance(time.Second)

	// "recent" is out of allowance and stays out: it was not the one evicted.
	require.ErrorIs(t, l.Allow(ctx, testScope, "recent"), token.SelectorRateLimited)
	clock.Advance(time.Second)

	// A third wallet would take the limiter past its cap of 2, so the oldest bucket goes.
	require.NoError(t, l.Allow(ctx, testScope, "new"))
	assert.Equal(t, 2, l.BucketCount())
	require.ErrorIs(t, l.Allow(ctx, testScope, "recent"), token.SelectorRateLimited)
	// "old" was evicted, so it starts over with a full bucket.
	require.NoError(t, l.Allow(ctx, testScope, "old"))
}

// TestBucketLimiter_IdleTimeoutFloor verifies the configured idle timeout is raised to at least
// the time a bucket needs to refill, so eviction cannot be used to skip the wait.
func TestBucketLimiter_IdleTimeoutFloor(t *testing.T) {
	// Refilling 10 requests at 1 request/s takes 10s, far more than the configured 1ms.
	c := Config{Rate: 1, Burst: 10, IdleTimeout: time.Millisecond}.normalize()
	assert.Equal(t, 10*time.Second, c.IdleTimeout)

	// An absurdly slow rate is capped instead of producing a nonsensical duration.
	c = Config{Rate: 1e-9, Burst: 1}.normalize()
	assert.Equal(t, maxIdleTimeout, c.IdleTimeout)
}

// TestBucketLimiter_Stop verifies Stop releases the buckets and leaves the limiter usable.
func TestBucketLimiter_Stop(t *testing.T) {
	l, _ := newTestLimiter(t, Config{Rate: 1, Burst: 1})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	require.ErrorIs(t, l.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)
	assert.Equal(t, 1, l.BucketCount())

	l.Stop()
	assert.Equal(t, 0, l.BucketCount())
	require.NoError(t, l.Allow(ctx, testScope, "alice"))
}

// TestDefaultConfig verifies the defaults documented for the enabled-without-values case.
func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	assert.InDelta(t, 100.0, c.Rate, 0)
	assert.Equal(t, 200, c.Burst)
	assert.Equal(t, DefaultIdleTimeout, c.IdleTimeout)
	assert.Equal(t, DefaultMaxBuckets, c.MaxBuckets)
}

// TestConfig_Normalize verifies non-positive configuration values fall back to the defaults, so
// that the zero Config is the default configuration.
func TestConfig_Normalize(t *testing.T) {
	c := Config{}.normalize()
	assert.InDelta(t, DefaultRate, c.Rate, 0)
	assert.Equal(t, 200, c.Burst)
	assert.Equal(t, DefaultMaxBuckets, c.MaxBuckets)
	assert.Equal(t, DefaultIdleTimeout, c.IdleTimeout)

	c = Config{Rate: -1, Burst: -1, MaxBuckets: -1, IdleTimeout: -time.Second}.normalize()
	assert.InDelta(t, DefaultRate, c.Rate, 0)
	assert.Equal(t, 200, c.Burst)
	assert.Equal(t, DefaultMaxBuckets, c.MaxBuckets)
	assert.Equal(t, DefaultIdleTimeout, c.IdleTimeout)

	// A fractional rate still yields a burst of at least one request.
	c = Config{Rate: 0.1}.normalize()
	assert.Equal(t, 1, c.Burst)
}

// TestBucketLimiter_RetryAfterInError verifies the denial error tells the caller how long to wait.
func TestBucketLimiter_RetryAfterInError(t *testing.T) {
	l, _ := newTestLimiter(t, Config{Rate: 2, Burst: 1})
	ctx := context.Background()

	require.NoError(t, l.Allow(ctx, testScope, "alice"))
	err := l.Allow(ctx, testScope, "alice")
	require.ErrorIs(t, err, token.SelectorRateLimited)
	// At 2 requests/s, one request is worth 500ms.
	assert.Contains(t, err.Error(), "retry in 500ms")
}

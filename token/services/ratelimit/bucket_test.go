/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testClock is a manually advanced clock, so that refill and eviction can be asserted without
// sleeping.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestBucketSet returns a set driven by clock, with the eviction goroutine already stopped so
// that only the explicit evictIdle calls in a test have any effect.
func newTestBucketSet(t *testing.T, rate, burst float64, idleTTL time.Duration, clock *testClock) *BucketSet {
	t.Helper()

	s := NewBucketSet(rate, burst, idleTTL, time.Hour, 0)
	t.Cleanup(s.Stop)
	s.now = clock.Now

	return s
}

// newTestBucketSetCapped is newTestBucketSet with an explicit maxKeys cap, for the tests that
// exercise capacity-bounded eviction.
func newTestBucketSetCapped(t *testing.T, rate, burst float64, idleTTL time.Duration, maxKeys int, clock *testClock) *BucketSet {
	t.Helper()

	s := NewBucketSet(rate, burst, idleTTL, time.Hour, maxKeys)
	t.Cleanup(s.Stop)
	s.now = clock.Now

	return s
}

func TestNewBucketSetDefaults(t *testing.T) {
	t.Run("burst below rate is raised to rate", func(t *testing.T) {
		s := NewBucketSet(10, 2, time.Minute, time.Hour, 0)
		t.Cleanup(s.Stop)
		assert.InDelta(t, 10.0, s.burst, 0)
	})

	t.Run("idle ttl covers a full refill", func(t *testing.T) {
		// 20 tokens at 2/s takes ten seconds to refill, which is longer than the requested TTL.
		s := NewBucketSet(2, 20, time.Second, time.Hour, 0)
		t.Cleanup(s.Stop)
		assert.Equal(t, 10*time.Second, s.idleTTL)
	})

	t.Run("non-positive idle ttl and cleanup interval fall back", func(t *testing.T) {
		s := NewBucketSet(1000, 1000, 0, 0, 0)
		t.Cleanup(s.Stop)
		assert.Equal(t, DefaultIdleTTL, s.idleTTL)
	})

	t.Run("non-positive rate is unmetered", func(t *testing.T) {
		s := NewBucketSet(0, 10, time.Minute, time.Minute, 0)
		t.Cleanup(s.Stop)
		assert.False(t, s.Metered())
		for range 100 {
			assert.True(t, s.Take("a"))
		}
		assert.Equal(t, 0, s.Len(), "an unmetered set should not accumulate buckets")
	})
}

func TestBucketSetTakeExhaustsAndRefills(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 2, 4, time.Hour, clock)

	require.True(t, s.Metered())
	for i := range 4 {
		assert.True(t, s.Take("alice"), "token %d should be available", i)
	}
	assert.False(t, s.Take("alice"), "the bucket should be empty")

	// Half a second at two tokens per second is one token.
	clock.advance(500 * time.Millisecond)
	assert.True(t, s.Take("alice"))
	assert.False(t, s.Take("alice"))

	// Refill never exceeds the capacity.
	clock.advance(time.Hour)
	for range 4 {
		assert.True(t, s.Take("alice"))
	}
	assert.False(t, s.Take("alice"))
}

func TestBucketSetKeysAreIndependent(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 1, 2, time.Hour, clock)

	require.True(t, s.Take("alice"))
	require.True(t, s.Take("alice"))
	require.False(t, s.Take("alice"))

	assert.True(t, s.Take("bob"), "bob must not pay for alice's traffic")
	assert.Equal(t, 2, s.Len())
}

func TestBucketSetSetRate(t *testing.T) {
	t.Run("clamps the balance to the new capacity", func(t *testing.T) {
		clock := newTestClock()
		s := newTestBucketSet(t, 10, 100, time.Hour, clock)

		// A full default bucket, then a quota cut to a quarter.
		require.True(t, s.Take("alice"))
		s.SetRate("alice", 2.5, 25)

		taken := 0
		for s.Take("alice") {
			taken++
			require.Less(t, taken, 100, "the reduced bucket must not hold the default capacity")
		}
		assert.Equal(t, 25, taken, "the balance should be clamped to the reduced capacity")
	})

	t.Run("refills at the reduced rate", func(t *testing.T) {
		clock := newTestClock()
		s := newTestBucketSet(t, 10, 10, time.Hour, clock)

		s.SetRate("alice", 1, 1)
		require.True(t, s.Take("alice"))
		require.False(t, s.Take("alice"))

		clock.advance(500 * time.Millisecond)
		assert.False(t, s.Take("alice"), "half a second is half a token at the reduced rate")
		clock.advance(500 * time.Millisecond)
		assert.True(t, s.Take("alice"))
	})

	t.Run("a non-positive rate is ignored", func(t *testing.T) {
		clock := newTestClock()
		s := newTestBucketSet(t, 1, 1, time.Hour, clock)

		s.SetRate("alice", 0, 0)
		require.True(t, s.Take("alice"))
		assert.False(t, s.Take("alice"), "the default quota should still apply")
	})

	t.Run("burst below rate is raised to rate", func(t *testing.T) {
		clock := newTestClock()
		s := newTestBucketSet(t, 10, 10, time.Hour, clock)

		s.SetRate("alice", 4, 1)
		taken := 0
		for s.Take("alice") {
			taken++
			require.Less(t, taken, 20, "the override capacity should be bounded")
		}
		assert.Equal(t, 4, taken)
	})
}

func TestBucketSetClearRateKeepsTheBalance(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 10, 10, time.Hour, clock)

	s.SetRate("alice", 1, 1)
	require.True(t, s.Take("alice"))
	require.False(t, s.Take("alice"))

	s.ClearRate("alice")
	assert.False(t, s.Take("alice"), "clearing an override must not hand back a full default bucket")

	clock.advance(time.Second)
	taken := 0
	for s.Take("alice") {
		taken++
		require.Less(t, taken, 20, "the default capacity should bound the refill")
	}
	assert.Equal(t, 10, taken, "the key should be back on the default rate")
}

func TestBucketSetClearRateOnUnknownKey(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 1, 1, time.Hour, clock)

	s.ClearRate("nobody")
	assert.Equal(t, 0, s.Len(), "clearing an unknown key must not create a bucket")
}

func TestBucketSetReset(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 10, 10, time.Hour, clock)

	s.SetRate("alice", 1, 1)
	require.True(t, s.Take("alice"))
	require.False(t, s.Take("alice"))

	s.Reset("alice")
	assert.Equal(t, 0, s.Len())
	taken := 0
	for s.Take("alice") {
		taken++
		require.Less(t, taken, 20, "a reset key should be back on the default capacity")
	}
	assert.Equal(t, 10, taken)
}

func TestBucketSetEvictIdle(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 10, 10, 30*time.Second, clock)

	require.True(t, s.Take("idle"))
	require.True(t, s.Take("throttled"))
	s.SetRate("throttled", 1, 1)
	require.Equal(t, 2, s.Len())

	clock.advance(31 * time.Second)
	s.evictIdle()

	assert.Equal(t, 1, s.Len(), "only the key without an override should be evicted")
	// The override survived, so the reduced quota is still in force.
	require.True(t, s.Take("throttled"))
	assert.False(t, s.Take("throttled"))
}

func TestBucketSetEvictIdleKeepsActiveKeys(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 10, 10, time.Minute, clock)

	require.True(t, s.Take("active"))
	clock.advance(30 * time.Second)
	require.True(t, s.Take("active"))
	clock.advance(31 * time.Second)
	s.evictIdle()

	assert.Equal(t, 1, s.Len(), "a key seen within the TTL should survive")
}

func TestBucketSetStopIsIdempotent(t *testing.T) {
	s := NewBucketSet(10, 10, time.Minute, time.Millisecond, 0)
	s.Stop()
	s.Stop()

	// A stopped set keeps enforcing its limits.
	for range 10 {
		assert.True(t, s.Take("alice"))
	}
	assert.False(t, s.Take("alice"))
}

func TestBucketSetCapBoundsKeyCount(t *testing.T) {
	clock := newTestClock()
	// A generous idleTTL so nothing is reclaimed by the idle sweep: only the cap can keep the
	// set small. This is the resource-exhaustion scenario — a flood of distinct, never-seen
	// keys — reduced to a count assertion.
	s := newTestBucketSetCapped(t, 100, 100, time.Hour, 10, clock)

	for i := range 5000 {
		clock.advance(time.Millisecond)
		s.Take(strconv.Itoa(i))
	}

	assert.Equal(t, 10, s.Len(), "the bucket set must never hold more than maxKeys buckets")
}

func TestBucketSetCapEvictsLeastRecentlyUsedUnmeteredBucket(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 2, clock)

	s.Take("a") // last = t0
	clock.advance(time.Second)
	s.Take("b") // last = t1
	clock.advance(time.Second)
	s.Take("a") // refreshes a: last = t2, so b is now the least-recently-used
	clock.advance(time.Second)

	s.Take("c") // at cap: evicts b (oldest), keeps a and c

	assert.Equal(t, 2, s.Len())
	_, hasA := s.buckets["a"]
	_, hasB := s.buckets["b"]
	_, hasC := s.buckets["c"]
	assert.True(t, hasA, "the recently-used key must be kept")
	assert.False(t, hasB, "the least-recently-used key must be evicted")
	assert.True(t, hasC, "the new key must be admitted")
}

func TestBucketSetCapNeverEvictsAnOverriddenBucket(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 2, clock)

	// Fill the set with two throttled (overridden) keys, each cut to a one-token quota.
	s.SetRate("x", 1, 1)
	s.SetRate("y", 1, 1)
	require.True(t, s.Take("x"))
	require.True(t, s.Take("y"))
	require.Equal(t, 2, s.Len())

	// A third key arrives with no unmetered bucket to evict. It must not displace a throttled
	// key (that would silently restore its quota) and must not grow the set past the cap: it is
	// served a transient, unstored bucket instead.
	assert.True(t, s.Take("z"), "a key served a transient bucket is unmetered")
	assert.Equal(t, 2, s.Len(), "an all-overridden set must not grow past the cap")
	_, hasZ := s.buckets["z"]
	assert.False(t, hasZ, "the transient bucket must not be stored")

	// The throttled keys kept their reduced quota: both were exhausted above and stay empty.
	assert.False(t, s.Take("x"), "the reduced quota of a throttled key must survive cap pressure")
	assert.False(t, s.Take("y"), "the reduced quota of a throttled key must survive cap pressure")
}

func TestBucketSetCapNonPositiveIsUnbounded(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 100, 100, time.Hour, 0, clock)

	for i := range 1000 {
		s.Take(strconv.Itoa(i))
	}

	assert.Equal(t, 1000, s.Len(), "a non-positive cap leaves the set unbounded")
}

func TestBucketSetConcurrentUse(t *testing.T) {
	s := NewBucketSet(1000, 1000, time.Minute, time.Millisecond, 0)
	t.Cleanup(s.Stop)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 200 {
				s.Take("shared")
				s.Take(strconv.Itoa(i))
				s.SetRate("shared", 100, 100)
				s.ClearRate("shared")
				s.Len()
			}
		}(i)
	}
	wg.Wait()
}

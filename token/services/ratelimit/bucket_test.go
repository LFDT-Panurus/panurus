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

	s := NewBucketSet(rate, burst, idleTTL, time.Hour, 0, clock.Now)
	t.Cleanup(s.Stop)

	return s
}

// newTestBucketSetCapped is newTestBucketSet with an explicit maxKeys cap, for the tests that
// exercise capacity-bounded eviction.
func newTestBucketSetCapped(t *testing.T, rate, burst float64, idleTTL time.Duration, maxKeys int, clock *testClock) *BucketSet {
	t.Helper()

	s := NewBucketSet(rate, burst, idleTTL, time.Hour, maxKeys, clock.Now)
	t.Cleanup(s.Stop)

	return s
}

func TestNewBucketSetDefaults(t *testing.T) {
	t.Run("burst below rate is raised to rate", func(t *testing.T) {
		s := NewBucketSet(10, 2, time.Minute, time.Hour, 0, nil)
		t.Cleanup(s.Stop)
		assert.InDelta(t, 10.0, s.burst, 0)
	})

	t.Run("idle ttl covers a full refill", func(t *testing.T) {
		// 20 tokens at 2/s takes ten seconds to refill, which is longer than the requested TTL.
		s := NewBucketSet(2, 20, time.Second, time.Hour, 0, nil)
		t.Cleanup(s.Stop)
		assert.Equal(t, 10*time.Second, s.idleTTL)
	})

	t.Run("non-positive idle ttl and cleanup interval fall back", func(t *testing.T) {
		s := NewBucketSet(1000, 1000, 0, 0, 0, nil)
		t.Cleanup(s.Stop)
		assert.Equal(t, DefaultIdleTTL, s.idleTTL)
	})

	t.Run("override budget is half of maxKeys", func(t *testing.T) {
		s := NewBucketSet(10, 10, time.Minute, time.Hour, 7, nil)
		t.Cleanup(s.Stop)
		assert.Equal(t, 3, s.maxOverrides, "the override budget should be maxKeys/2")
	})

	t.Run("an unbounded set has no override budget", func(t *testing.T) {
		s := NewBucketSet(10, 10, time.Minute, time.Hour, 0, nil)
		t.Cleanup(s.Stop)
		assert.Equal(t, 0, s.maxOverrides)
		assert.True(t, s.overrideBudgetAvailable(), "an unbounded set caps nothing")
	})

	t.Run("non-positive rate is unmetered", func(t *testing.T) {
		s := NewBucketSet(0, 10, time.Minute, time.Minute, 0, nil)
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

	t.Run("an unmetered set is left untouched", func(t *testing.T) {
		// An unmetered set never consults its buckets, so an override would enforce nothing and
		// leak a bucket that no sweep reclaims. SetRate must not store one.
		s := NewBucketSet(0, 10, time.Minute, time.Minute, 0, nil)
		t.Cleanup(s.Stop)

		s.SetRate("alice", 1, 1)
		assert.Equal(t, 0, s.Len(), "SetRate on an unmetered set must not create a bucket")
		assert.True(t, s.Take("alice"), "an unmetered set stays unmetered after SetRate")
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

func TestBucketSetSubUnitRateThrottlesNotLocksOut(t *testing.T) {
	t.Run("default sub-1 rate", func(t *testing.T) {
		clock := newTestClock()
		// 0.5 tokens/s with a zero burst: the capacity floor of 1 keeps the key takeable instead
		// of locking it out (a bucket below one whole token could never satisfy Take).
		s := newTestBucketSet(t, 0.5, 0, time.Hour, clock)
		assert.InDelta(t, 1.0, s.burst, 0, "burst must be floored to one whole token")

		require.True(t, s.Take("alice"), "a fresh bucket must yield its first token, not lock out")
		require.False(t, s.Take("alice"), "half a token per second holds only one at a time")

		clock.advance(2 * time.Second) // two seconds is one token at 0.5/s
		assert.True(t, s.Take("alice"), "the key should throttle, not stay locked out")
	})

	t.Run("sub-1 override", func(t *testing.T) {
		clock := newTestClock()
		s := newTestBucketSet(t, 10, 10, time.Hour, clock)

		require.True(t, s.SetRate("alice", 0.5, 0.5))
		require.True(t, s.Take("alice"), "a sub-1 override must throttle, not lock the principal out")
		assert.False(t, s.Take("alice"))

		clock.advance(2 * time.Second)
		assert.True(t, s.Take("alice"), "the override refills at 0.5/s rather than locking out")
	})
}

func TestBucketSetSetRateReportsWhetherApplied(t *testing.T) {
	t.Run("applied on a metered set with budget", func(t *testing.T) {
		s := NewBucketSet(10, 10, time.Minute, time.Hour, 4, nil)
		t.Cleanup(s.Stop)
		assert.True(t, s.SetRate("alice", 1, 1))
	})

	t.Run("dropped on an unmetered set", func(t *testing.T) {
		s := NewBucketSet(0, 10, time.Minute, time.Hour, 4, nil)
		t.Cleanup(s.Stop)
		assert.False(t, s.SetRate("alice", 1, 1))
	})

	t.Run("dropped for a non-positive rate", func(t *testing.T) {
		s := NewBucketSet(10, 10, time.Minute, time.Hour, 4, nil)
		t.Cleanup(s.Stop)
		assert.False(t, s.SetRate("alice", 0, 0))
	})

	t.Run("dropped when maxKeys is one leaves no override budget", func(t *testing.T) {
		clock := newTestClock()
		s := newTestBucketSetCapped(t, 10, 10, time.Hour, 1, clock)
		require.Equal(t, 0, s.maxOverrides, "a single slot cannot both carry an override and leave a victim")
		assert.False(t, s.SetRate("alice", 1, 1), "SetRate must report the throttle was dropped, not throttle silently")

		// The drop is reported only through the return value: the key keeps the default quota.
		taken := 0
		for s.Take("alice") {
			taken++
			require.Less(t, taken, 20)
		}
		assert.Equal(t, 10, taken, "a dropped override leaves the key on the default quota")
	})

	t.Run("dropped when the override budget is spent", func(t *testing.T) {
		s := NewBucketSet(10, 10, time.Minute, time.Hour, 4, nil) // budget = maxKeys/2 = 2
		t.Cleanup(s.Stop)
		require.True(t, s.SetRate("a", 1, 1))
		require.True(t, s.SetRate("b", 1, 1))
		assert.False(t, s.SetRate("c", 1, 1), "a third override exceeds the maxKeys/2 budget")
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
	s := NewBucketSet(10, 10, time.Minute, time.Millisecond, 0, nil)
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
	// maxKeys 4 reserves two override slots (maxKeys/2); the other two stay non-overridden.
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 4, clock)

	// Spend the override budget on two throttled keys, each cut to a one-token quota, then fill
	// the remaining slots with ordinary (non-overridden) keys.
	s.SetRate("x", 1, 1)
	s.SetRate("y", 1, 1)
	require.True(t, s.Take("x"))
	require.True(t, s.Take("y"))
	s.Take("a") // non-overridden, last = t0 (oldest)
	clock.advance(time.Second)
	s.Take("b") // non-overridden, last = t1
	require.Equal(t, 4, s.Len())

	// A fifth key arrives at the cap. It must evict the least-recently-used non-overridden bucket
	// ("a"), never a throttled key, and must not grow the set past the cap.
	clock.advance(time.Second)
	s.Take("z")

	assert.Equal(t, 4, s.Len(), "the set must not grow past the cap")
	_, hasA := s.buckets["a"]
	_, hasZ := s.buckets["z"]
	assert.False(t, hasA, "the least-recently-used non-overridden key must be evicted")
	assert.True(t, hasZ, "the new key must be admitted into the reclaimed slot")

	// The throttled keys were never evicted, so their reduced quota is still in force.
	require.Contains(t, s.buckets, "x", "a throttled key must never be evicted")
	require.Contains(t, s.buckets, "y", "a throttled key must never be evicted")
	assert.True(t, s.buckets["x"].overridden, "the override must survive cap pressure")
	assert.InDelta(t, 1.0, s.buckets["x"].burst, 0, "the reduced capacity must survive cap pressure")
}

// TestBucketSetCapSetRateOnFreshKeysStaysBounded guards against SetRate growing the set without
// limit. Overridden buckets are never evicted (not even by the idle sweep), so if SetRate stored
// its override regardless of the cap, a flood of distinct keys each tripping a SetRate would grow
// the map forever — the unbounded allocation maxKeys exists to prevent. Once the override budget
// (maxKeys/2) is spent a further SetRate on a fresh key must be dropped, leaving it on the default.
func TestBucketSetCapSetRateOnFreshKeysStaysBounded(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 4, clock)

	// Spend the whole override budget, then keep calling SetRate on brand-new keys.
	s.SetRate("x", 1, 1)
	s.SetRate("y", 1, 1)
	require.Equal(t, 2, s.overrides)
	for i := range 100 {
		s.SetRate("fresh-"+strconv.Itoa(i), 1, 1)
	}

	assert.Equal(t, 2, s.overrides, "SetRate on fresh keys must not add overrides past the budget")
	assert.LessOrEqual(t, s.Len(), 4, "the set must never grow past maxKeys")

	// The dropped override left the key on the set's default quota, not the reduced one: a fresh
	// default bucket holds the full burst, so its first Take succeeds.
	assert.True(t, s.Take("fresh-0"), "a dropped override leaves the key on the default quota")
}

// TestBucketSetCapOverrideBudgetBlocksPromotion checks that an existing non-overridden key cannot
// be promoted to an override once the budget is spent: it stays on the default quota.
func TestBucketSetCapOverrideBudgetBlocksPromotion(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 2, clock) // maxOverrides = 1

	s.Take("x")          // non-overridden
	s.SetRate("a", 1, 1) // spends the one override slot
	require.Equal(t, 1, s.overrides)

	s.SetRate("x", 1, 1) // budget spent: promotion must be refused
	assert.Equal(t, 1, s.overrides, "promotion past the budget must not add an override")
	require.Contains(t, s.buckets, "x")
	assert.False(t, s.buckets["x"].overridden, "x must stay on the default quota")
}

// TestBucketSetCapKeepsDefaultLimiterUnderOverridePressure is the security property behind the
// override budget: when many keys are throttled, a fresh principal must still be metered on the
// default quota rather than served an unmetered transient bucket. Without the budget, overrides
// could fill every slot (maxKeys=2 here would take two), leaving bucketFor nothing to evict and
// turning the default limit off for everyone else.
func TestBucketSetCapKeepsDefaultLimiterUnderOverridePressure(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 2, clock)

	// Drive as many keys into overrides as an attacker could; the budget admits only maxKeys/2.
	for i := range 50 {
		s.SetRate("throttled-"+strconv.Itoa(i), 1, 1)
	}
	require.Equal(t, 1, s.overrides, "overrides must be capped at maxKeys/2")

	// A fresh principal is metered on the default quota: it drains its burst and is then denied,
	// never unmetered.
	for i := range 10 {
		require.True(t, s.Take("victim"), "token %d should come from a fresh default bucket", i)
	}
	assert.False(t, s.Take("victim"), "the default limit must still deny once the bucket is drained")
}

// TestBucketSetCapEvictsEmptyStringVictim guards against the empty string being treated as a
// "no victim found" sentinel. "" is a legitimate key, and when it is the only non-overridden
// bucket it must be evicted to make room — otherwise the new key is served a transient,
// unstored bucket and stays permanently unmetered despite an evictable victim existing.
func TestBucketSetCapEvictsEmptyStringVictim(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 2, clock)

	s.Take("")           // "" non-overridden, last = t0 — the only evictable victim
	s.SetRate("a", 1, 1) // "a" overridden, must never be evicted
	require.True(t, s.Take("a"))
	require.Equal(t, 2, s.Len())

	clock.advance(time.Second)
	s.Take("c") // at cap: "a" is overridden, so "" is the only victim and must be reclaimed

	assert.Equal(t, 2, s.Len(), "the set must not grow past the cap")
	_, hasEmpty := s.buckets[""]
	_, hasC := s.buckets["c"]
	assert.False(t, hasEmpty, "the empty-string victim must be evicted, not skipped as a sentinel")
	assert.True(t, hasC, "the new key must be admitted into the reclaimed slot")
}

// TestBucketSetCapEvictsLRUWhenEmptyStringIsNewest guards against "" (when it is the newest,
// most-recently-used bucket) being evicted in place of the true LRU because of the sentinel
// collision.
func TestBucketSetCapEvictsLRUWhenEmptyStringIsNewest(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 2, clock)

	s.Take("x") // last = t0 — the least-recently-used
	clock.advance(time.Second)
	s.Take("") // last = t1 — the most-recently-used
	clock.advance(time.Second)

	s.Take("c") // at cap: evicts "x" (LRU), keeps "" and "c"

	assert.Equal(t, 2, s.Len())
	_, hasX := s.buckets["x"]
	_, hasEmpty := s.buckets[""]
	_, hasC := s.buckets["c"]
	assert.False(t, hasX, "the least-recently-used key must be evicted")
	assert.True(t, hasEmpty, "the most-recently-used empty-string key must be kept")
	assert.True(t, hasC, "the new key must be admitted")
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
	s := NewBucketSet(1000, 1000, time.Minute, time.Millisecond, 0, nil)
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

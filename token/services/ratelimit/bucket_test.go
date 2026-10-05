/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"math"
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
	// A bounded set reclaims idle overrides too (once they have refilled), so an abandoned override
	// cannot hold an override-budget slot forever and brick escalation. The idle sweep evicts both
	// the plain idle key and the idle, refilled override.
	s := newTestBucketSetCapped(t, 10, 10, 30*time.Second, 4, clock)

	require.True(t, s.Take("idle"))
	require.True(t, s.Take("throttled"))
	s.SetRate("throttled", 1, 1)
	require.Equal(t, 2, s.Len())
	require.Equal(t, 1, s.overrides)

	clock.advance(31 * time.Second)
	s.evictIdle()

	assert.Equal(t, 0, s.Len(), "an idle plain key and an idle, refilled override are both reclaimed")
	assert.Equal(t, 0, s.overrides, "the reclaimed override frees its budget slot")
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

// TestBucketSetCapNeverEvictsAStillThrottledBucket is the rate-limit-bypass guard on the cap path.
// evictOneForRoom must refuse to evict a non-overridden bucket that has not refilled to capacity,
// because bucketFor recreates an evicted key with a full bucket — so evicting a still-draining key
// and letting the attacker recreate it would hand back a fresh budget. An attacker interleaving
// maxKeys junk keys between bursts must therefore be denied, not served, until those junk buckets
// refill and become legitimately evictable.
func TestBucketSetCapNeverEvictsAStillThrottledBucket(t *testing.T) {
	clock := newTestClock()
	// rate=1, burst=5, maxKeys=8: the review's reproduction. A generous idleTTL so only the cap,
	// never the idle sweep, can reclaim.
	s := newTestBucketSetCapped(t, 1, 5, time.Hour, 8, clock)

	// Drain key A completely.
	for range 5 {
		require.True(t, s.Take("A"))
	}
	require.False(t, s.Take("A"), "A is now exhausted")

	// One second later A's honest budget is exactly one token. An attacker issues 8 Takes on
	// throwaway keys to push A to the least-recently-used slot and force it out.
	clock.advance(time.Second)
	for i := range 8 {
		s.Take("junk-" + strconv.Itoa(i))
	}

	// A must not have been evicted-and-refreshed: it either survived (and so still meters at its
	// true ~1-token budget) or, if it was dropped, recreating it is refused rather than handed a
	// full 5-token bucket. Either way the attacker gets at most one token, never five.
	taken := 0
	for s.Take("A") {
		taken++
		require.Less(t, taken, 5, "a throttled key must never be reset to a full bucket by cap eviction")
	}
	assert.LessOrEqual(t, taken, 1, "A's honest one-second budget is one token, not a fresh burst")
}

// TestBucketSetCapDeniesWhenNoBucketHasRefilled checks the fail-closed outcome: when every
// non-overridden bucket is still mid-refill, a brand-new key at the cap is admitted by displacing
// the least-recently-used bucket but seeded EMPTY - so it is metered from zero rather than served an
// unmetered (full) bucket, and the eviction creates no budget. Its first Take is therefore denied
// (the bucket is empty), but it recovers at the default rate instead of staying hard-denied.
func TestBucketSetCapAdmitsNewKeyEmptyWhenNoBucketHasRefilled(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 1, 5, time.Hour, 2, clock)

	// Fill both slots and drain each, so neither bucket is at capacity.
	for _, k := range []string{"a", "b"} {
		for range 5 {
			require.True(t, s.Take(k))
		}
	}
	require.Equal(t, 2, s.Len())

	// A fresh key arrives at the cap while both existing buckets are still draining. It is admitted
	// by displacing the LRU bucket, but seeded empty: its first Take is denied because the bucket
	// holds no tokens, not because it was served unmetered or grew the set past the cap.
	assert.False(t, s.Take("c"), "a new key seeded empty has no token to give on its first Take")
	assert.Equal(t, 2, s.Len(), "the set must not grow past the cap")
	require.Contains(t, s.buckets, "c", "the new key is admitted, not hard-denied")
	assert.InDelta(t, 0.0, s.buckets["c"].tokens, 0, "and seeded empty, so eviction creates no budget")

	// It recovers at the default rate rather than staying denied: one second is one token.
	clock.advance(time.Second)
	assert.True(t, s.Take("c"), "the admitted key is metered from zero and refills at the default rate")
}

// TestBucketSetCapNewKeyNotDoSedByDrainedSlots is the security property behind fix 5: an attacker
// who fills every slot with drained throwaway keys must not be able to lock out a never-seen honest
// principal. Denying the newcomer outright would charge the fail-closed cost to the victim, not the
// abuser - a cheap DoS of the signing path. The newcomer must instead displace the abuser's LRU
// bucket and be metered from an empty bucket, so that under sustained load it is served at the
// default rate rather than served nothing at all.
func TestBucketSetCapNewKeyNotDoSedByDrainedSlots(t *testing.T) {
	clock := newTestClock()
	// maxKeys=8, rate=10, burst=10: the review's reproduction.
	s := newTestBucketSetCapped(t, 10, 10, time.Hour, 8, clock)

	// An attacker drains eight junk keys, filling every slot with a mid-refill bucket.
	for i := range 8 {
		k := "junk-" + strconv.Itoa(i)
		for range 10 {
			require.True(t, s.Take(k))
		}
	}
	require.Equal(t, 8, s.Len())

	// A never-seen honest principal now generates sustained load. Over ten seconds at rate=10 it
	// must be served roughly its fair share, not locked out at zero.
	served := 0
	for range 100 {
		clock.advance(100 * time.Millisecond) // 10s total
		if s.Take("honest-user") {
			served++
		}
	}

	assert.Positive(t, served, "an honest newcomer must not be DoSed to zero by an attacker's drained slots")
	assert.LessOrEqual(t, s.Len(), 8, "admitting the newcomer must not grow the set past the cap")
	// It is metered, not unmetered: ten seconds at rate=10 is about 100 tokens, never a free burst.
	assert.LessOrEqual(t, served, 101, "the admitted key is rate-limited, not served unmetered")
}

// TestBucketSetUnboundedReclaimsIdleOverrides covers override reclamation on an unbounded set. With
// no maxKeys cap, evictOneForRoom never runs and overrides are bounded by nothing but time: an
// override idle for idleTTL must be reclaimed by the idle sweep, or a key-rotating caller minting
// one override per identity leaks a permanent bucket each.
func TestBucketSetUnboundedReclaimsIdleOverrides(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSet(t, 10, 10, 30*time.Second, clock) // unbounded (maxKeys = 0)

	require.True(t, s.SetRate("throttled", 1, 1))
	require.True(t, s.Take("idle"))
	require.Equal(t, 2, s.Len())
	require.Equal(t, 1, s.overrides)

	clock.advance(31 * time.Second)
	s.evictIdle()

	assert.Equal(t, 0, s.Len(), "an unbounded set must reclaim both idle plain and idle overridden buckets")
	assert.Equal(t, 0, s.overrides, "the reclaimed override must be uncounted")
}

// TestBucketSetEvictIdleKeepsMidRefillOverride is the refill-guard counterpart: the idle sweep must
// not reclaim an override that is idle past idleTTL but has not yet refilled to its own (slower)
// capacity. idleTTL is clamped from the default rate only, so an override with a far slower refill
// can be idle longer than idleTTL while still metering. Reclaiming it would recreate the key full at
// the default quota (bucketFor seeds s.burst), handing back far more budget than the override
// allowed and letting a principal that bursts once per idleTTL escape escalation entirely.
func TestBucketSetEvictIdleKeepsMidRefillOverride(t *testing.T) {
	clock := newTestClock()
	// Default 100/100 → idleTTL stays the requested 30s (a default bucket refills in 1s). The
	// override refills at 0.001/s, so one token takes 1000s - far longer than idleTTL.
	s := newTestBucketSetCapped(t, 100, 100, 30*time.Second, 4, clock)

	require.True(t, s.SetRate("throttled", 0.001, 1))
	require.True(t, s.Take("throttled"), "drain the single override token so it is mid-refill")
	require.False(t, s.Take("throttled"))
	require.Equal(t, 1, s.overrides)

	clock.advance(31 * time.Second) // past idleTTL, but the override is nowhere near refilled
	s.evictIdle()

	require.Contains(t, s.buckets, "throttled", "a mid-refill override must not be reclaimed early")
	assert.Equal(t, 1, s.overrides, "its budget slot is still held while it meters")
	assert.True(t, s.buckets["throttled"].overridden, "the override is retained, not silently cleared")
	assert.False(t, s.Take("throttled"), "and it still enforces the reduced quota, not a full default bucket")

	// Once the override has refilled to its own capacity it becomes reclaimable like any idle key.
	clock.advance(1001 * time.Second)
	s.evictIdle()
	assert.NotContains(t, s.buckets, "throttled", "a refilled, idle override is reclaimed")
	assert.Equal(t, 0, s.overrides, "the reclaimed override frees its budget slot")
}

func TestBucketSetCapNonPositiveIsUnbounded(t *testing.T) {
	clock := newTestClock()
	s := newTestBucketSetCapped(t, 100, 100, time.Hour, 0, clock)

	for i := range 1000 {
		s.Take(strconv.Itoa(i))
	}

	assert.Equal(t, 1000, s.Len(), "a non-positive cap leaves the set unbounded")
}

// TestBucketSetNonFiniteRateIsUnmetered guards against a non-finite rate silently failing OPEN. A
// NaN rate (e.g. from a 0/0 derived limit) or an infinity used to slip past the rate<=0 unmetered
// check yet fail every rate>0 test, so the set ran its eviction goroutine while Metered reported
// unmetered and Take never denied. It must now collapse to a consistently unmetered set.
func TestBucketSetNonFiniteRateIsUnmetered(t *testing.T) {
	for _, rate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		s := NewBucketSet(rate, 10, time.Minute, time.Minute, 0, nil)
		t.Cleanup(s.Stop)

		assert.False(t, s.Metered(), "a non-finite rate must be reported as unmetered, not metered")
		for range 100 {
			assert.True(t, s.Take("a"), "an unmetered set admits every request")
		}
		assert.Equal(t, 0, s.Len(), "an unmetered set must not accumulate buckets")
		assert.False(t, s.SetRate("a", 1, 1), "SetRate on the resulting unmetered set is a no-op")
	}
}

// TestBucketSetSetRateRejectsNonFiniteRate guards that a per-key override cannot be given a NaN or
// infinite rate, which would make that one key's bucket never deny (a single-key fail-open) and
// leak a permanent, eviction-exempt bucket.
func TestBucketSetSetRateRejectsNonFiniteRate(t *testing.T) {
	for _, rate := range []float64{math.NaN(), math.Inf(1)} {
		s := NewBucketSet(10, 10, time.Minute, time.Hour, 4, nil)
		t.Cleanup(s.Stop)

		assert.False(t, s.SetRate("alice", rate, rate), "a non-finite override rate must be rejected")
		assert.Equal(t, 0, s.overrides, "a rejected override must not be counted or stored")
		// The key stays on the finite default quota: it drains its burst and is then denied.
		for range 10 {
			require.True(t, s.Take("alice"))
		}
		assert.False(t, s.Take("alice"), "the default quota must still deny once drained")
	}
}

// TestBucketSetExtremeBurstRateDoesNotOverflowIdleTTL guards the float->Duration conversion in the
// idleTTL refill-floor: an enormous burst/rate used to overflow int64 and wrap to a NEGATIVE
// Duration, which silently skipped the clamp. It must now cap at a large positive TTL instead.
func TestBucketSetExtremeBurstRateDoesNotOverflowIdleTTL(t *testing.T) {
	s := NewBucketSet(1e-6, 1e18, time.Minute, time.Hour, 0, nil)
	t.Cleanup(s.Stop)

	assert.Positive(t, s.idleTTL, "the refill-floor clamp must never leave a negative idleTTL")
	assert.GreaterOrEqual(t, s.idleTTL, time.Minute, "idleTTL must still cover at least the requested TTL")
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

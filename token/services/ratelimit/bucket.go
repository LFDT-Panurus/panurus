/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package ratelimit provides a reusable set of per-key token buckets.
//
// It carries no policy of its own: it neither decides what a key is nor what happens when a
// key runs out of tokens, so the same mechanism serves callers whose quota is fixed (a plain
// rate limit) and callers that adjust a single key's quota at runtime (an escalating
// throttle - see token/services/identity/throttle).
package ratelimit

import (
	"math"
	"sync"
	"time"
)

const (
	// DefaultIdleTTL is how long a key's bucket is kept after its last request before being
	// evicted, so that memory stays proportional to the set of recently active keys rather
	// than to all keys ever seen.
	DefaultIdleTTL = 10 * time.Minute
	// DefaultCleanupInterval is how often idle buckets are swept.
	DefaultCleanupInterval = time.Minute
)

// BucketSet is a set of per-key token buckets. Every key gets its own bucket, created full
// on first use and refilled at rate tokens per second up to burst tokens, so one key's
// traffic never consumes another's budget. Buckets are created lazily and evicted once
// idle, bounding memory to the recently active keys.
//
// A BucketSet is safe for concurrent use by multiple goroutines.
type BucketSet struct {
	// rate is the default refill speed in tokens per second. When it is not positive the
	// set is unmetered and Take always succeeds.
	rate float64
	// burst is the default bucket capacity in tokens.
	burst float64
	// idleTTL is how long a bucket without an override survives without requests.
	idleTTL time.Duration
	// now is the clock, indirected for tests.
	now func() time.Time

	// mu guards buckets and the state of each bucket in it. A single mutex is enough: the
	// critical section is a map lookup and a handful of float operations.
	mu      sync.Mutex
	buckets map[string]*bucket

	stopOnce sync.Once
	stopped  chan struct{}
}

// bucket is one key's token bucket. tokens is the balance as of last. When overridden is
// set, rate and burst replace the set's defaults for this key alone and the bucket is
// exempt from idle eviction, so that a reduced quota is never silently restored.
type bucket struct {
	tokens     float64
	last       time.Time
	rate       float64
	burst      float64
	overridden bool
}

// NewBucketSet returns a set whose buckets refill at rate tokens per second with a capacity
// of burst tokens.
//
// Zero or negative values select sensible substitutes: a non-positive rate yields an
// unmetered set, a burst below rate is raised to rate (a bucket must hold at least one
// second's worth of refill to sustain that rate), and a non-positive idleTTL or
// cleanupInterval falls back to DefaultIdleTTL / DefaultCleanupInterval.
//
// Call Stop when the set is no longer needed to release its eviction goroutine.
func NewBucketSet(rate, burst float64, idleTTL, cleanupInterval time.Duration) *BucketSet {
	s := &BucketSet{
		rate:    rate,
		burst:   math.Max(burst, rate),
		idleTTL: idleTTL,
		now:     time.Now,
		buckets: make(map[string]*bucket),
		stopped: make(chan struct{}),
	}

	if s.rate <= 0 {
		// Nothing to meter and nothing to evict: no goroutine is started, and Stop stays
		// safe to call.
		return s
	}

	if cleanupInterval <= 0 {
		cleanupInterval = DefaultCleanupInterval
	}
	if s.idleTTL <= 0 {
		s.idleTTL = DefaultIdleTTL
	}
	// Evicting a bucket resets it to full, which is only free once it would have refilled
	// completely anyway. Keep idle buckets at least that long so eviction can never hand a
	// throttled key a fresh budget.
	if refill := time.Duration(s.burst / s.rate * float64(time.Second)); s.idleTTL < refill {
		s.idleTTL = refill
	}

	go s.evictLoop(cleanupInterval)

	return s
}

// Metered reports whether the set enforces any limit at all. An unmetered set (built with a
// non-positive rate) lets every Take succeed.
func (s *BucketSet) Metered() bool {
	return s.rate > 0
}

// Take refills key's bucket for the elapsed time and consumes one token from it, reporting
// whether a token was available. An unmetered set always reports true.
func (s *BucketSet) Take(key string) bool {
	if s.rate <= 0 {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b := s.bucketFor(key)
	if b.tokens < 1 {
		return false
	}
	b.tokens--

	return true
}

// SetRate replaces the quota of a single key with rate tokens per second and a capacity of
// burst, leaving every other key on the set's defaults. It is how a caller narrows the
// budget of one misbehaving principal without rebuilding the set.
//
// The current balance is clamped to the new capacity, so lowering a quota cannot hand the
// key more tokens than the new bucket holds; raising it never grants the difference
// retroactively either, the bucket simply refills faster from where it is. A key with an
// override is kept until ClearRate is called, so idle eviction cannot restore the default
// quota behind the caller's back.
//
// A non-positive rate is ignored: an unmetered exception for a single key would be a
// footgun, and the caller that wants one can stop consulting the set for that key.
func (s *BucketSet) SetRate(key string, rate, burst float64) {
	if rate <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b := s.bucketFor(key)
	b.rate = rate
	b.burst = math.Max(burst, rate)
	b.overridden = true
	b.tokens = math.Min(b.tokens, b.burst)
}

// ClearRate drops key's quota override, returning it to the set's defaults and making it
// eligible for idle eviction again. The balance is kept, and clamped to the default
// capacity: a key coming back from a reduced quota refills towards the default rather than
// jumping straight to a full default bucket.
func (s *BucketSet) ClearRate(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[key]
	if !ok {
		return
	}
	b.rate = s.rate
	b.burst = s.burst
	b.overridden = false
	b.tokens = math.Min(b.tokens, s.burst)
}

// Reset discards key's bucket, including any quota override, so its next Take starts from a
// full default bucket. It is meant for tests and for administrative "forgive this key"
// actions, not for the metering path.
func (s *BucketSet) Reset(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.buckets, key)
}

// Len returns the number of buckets currently held. It is exported for tests and for
// gauges reporting how much state the set has accumulated.
func (s *BucketSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.buckets)
}

// EvictIdleNow runs one eviction sweep immediately, outside of the background ticker. It is
// intended for tests that need deterministic control over when idle buckets are reclaimed.
func (s *BucketSet) EvictIdleNow() {
	s.evictIdle()
}

// SetNow replaces the clock used by the set. It is intended for tests that need a manually
// advanced clock; callers must call it before any other method on the set.
func (s *BucketSet) SetNow(fn func() time.Time) {
	s.now = fn
}

// Stop terminates the eviction goroutine. Buckets are left in place, so a set that is still
// consulted after Stop keeps enforcing its limits; it simply stops reclaiming the memory of
// idle keys. Stop is idempotent.
func (s *BucketSet) Stop() {
	s.stopOnce.Do(func() { close(s.stopped) })
}

// bucketFor returns key's bucket, creating it full when the key is new and refilling it for
// the time elapsed since its last update. Callers must hold s.mu.
func (s *BucketSet) bucketFor(key string) *bucket {
	now := s.now()
	b, ok := s.buckets[key]
	if !ok {
		// A key not seen recently starts with a full bucket at the set's defaults.
		b = &bucket{tokens: s.burst, last: now, rate: s.rate, burst: s.burst}
		s.buckets[key] = b

		return b
	}

	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(b.burst, b.tokens+elapsed.Seconds()*b.rate)
		b.last = now
	}

	return b
}

// evictLoop sweeps idle buckets until Stop is called.
func (s *BucketSet) evictLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopped:
			return
		case <-ticker.C:
			s.evictIdle()
		}
	}
}

// evictIdle drops the buckets of keys that have made no request within idleTTL. Such a
// bucket has already refilled to capacity, so dropping it loses no accounting. Keys with a
// quota override are skipped as a safety net: the caller is expected to call ClearRate before
// evicting a principal, but if it does not, the bucket is retained rather than silently
// restoring the default quota for a key that is still being throttled.
func (s *BucketSet) evictIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := s.now().Add(-s.idleTTL)
	for key, b := range s.buckets {
		if !b.overridden && b.last.Before(cutoff) {
			delete(s.buckets, key)
		}
	}
}

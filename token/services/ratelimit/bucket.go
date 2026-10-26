/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package ratelimit provides a reusable set of per-key token buckets.
//
// It carries no policy of its own: it neither decides what a key is nor what happens when a
// key runs out of tokens, so the same mechanism serves callers whose quota is fixed (a plain
// rate limit) and callers that adjust a single key's quota at runtime (an escalating throttle
// layered on top of SetRate/ClearRate).
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
// When the keys are attacker-supplied the idle sweep alone bounds memory only by time, not by
// count: a flood of distinct keys allocates a bucket each, all retained until idleTTL elapses.
// maxKeys caps the number of buckets held at once so the set cannot be driven into unbounded
// allocation, evicting the least-recently-used non-overridden bucket that has refilled to capacity
// to make room (see bucketFor). A bucket still mid-refill is never evicted: recreating its key
// would seed a full bucket and hand a throttled principal a fresh budget, so when every evictable
// bucket is still refilling bucketFor fails closed and the request is denied rather than served
// unmetered.
//
// On a bounded set, overridden buckets are never evicted: without a further limit a flood of
// throttled keys could fill every slot and leave bucketFor no bucket to evict — at which point
// fresh keys would be served transient, unmetered buckets and the default limit would quietly
// disengage for every other principal. maxOverrides caps overrides at half of maxKeys to keep a
// non-overridden floor, so an evictable bucket always exists and the limiter stays on even under
// override pressure. On an unbounded set there is no override count cap; instead the idle sweep
// reclaims an override idle for idleTTL, so a key-rotating caller that mints an override per
// identity cannot leak buckets without bound (see evictIdle).
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
	// maxKeys caps the number of buckets held simultaneously. When it is not positive the set
	// is unbounded and relies on idle eviction alone.
	maxKeys int
	// maxOverrides caps how many buckets may carry a quota override at once, at maxKeys/2, so that
	// at least half the slots stay non-overridden and evictOneForRoom has a candidate to evict
	// whenever one of them has refilled. This keeps the default limit from disengaging when many
	// keys are throttled at once (eviction may still fail closed if every non-overridden bucket is
	// mid-refill, which denies the new request rather than serving it unmetered). It is
	// zero and unused for an unbounded set (maxKeys not positive), which bounds overrides not by
	// count but by time: the idle sweep reclaims an override idle for idleTTL (see evictIdle).
	maxOverrides int
	// now is the clock, indirected for tests. It is set once by NewBucketSet before the eviction
	// goroutine starts and never written again, so it needs no lock despite being read under s.mu.
	now func() time.Time

	// mu guards buckets, overrides, and the state of each bucket in it. A single mutex is
	// enough: the critical section is a map lookup and a handful of float operations.
	mu sync.Mutex
	// overrides is the number of buckets currently carrying a quota override, kept in step with
	// the overridden flag so SetRate can honour maxOverrides without scanning the map.
	overrides int
	buckets   map[string]*bucket

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

// floorBurst raises a bucket capacity to at least the larger of rate and 1.
//
// The floor of 1 keeps the bucket usable at all: Take consumes a whole token, so a capacity below
// 1 could never be taken from, and a sub-1 rate would lock the key out entirely instead of
// throttling it.
//
// The rate floor keeps the configured rate actually attainable under coarse-grained arrival. A
// continuously refilled bucket's long-run throughput is the rate regardless of capacity (any
// capacity >= 1 sustains it under perfectly paced, one-request-at-a-time arrival), so capacity
// bounds only the burst - but a client that batches its requests, arriving once per refill window
// rather than evenly, can draw at most burst tokens per window. With burst < rate such a client is
// capped at burst/second and can never reach the rate it was granted. Flooring burst to rate makes
// the configured rate reachable however coarsely the client batches within a one-second window.
//
// The trade-off is that a burst tighter than the rate cannot be configured: NewBucketSet(100, 5,
// ...) yields burst == 100, not 5. A caller that genuinely wants a sub-rate burst (tolerating the
// reduced effective rate that implies for a batching client) is not served by this set.
func floorBurst(burst, rate float64) float64 {
	// A NaN or infinite burst is not a usable capacity: Take would never deny for such a bucket
	// (NaN < 1 and +Inf < 1 are both false), failing open. Treat it as below the floor and clamp
	// it down rather than trust it.
	floor := math.Max(rate, 1)
	if !(burst > floor) || math.IsInf(burst, 1) {
		return floor
	}

	return burst
}

// NewBucketSet returns a set whose buckets refill at rate tokens per second with a capacity
// of burst tokens, holding at most maxKeys buckets at once.
//
// Zero or negative values select sensible substitutes: a non-positive rate yields an
// unmetered set; burst is raised to the larger of rate and 1 (a bucket must hold at least one
// second's worth of refill to sustain its rate, and at least one whole token or Take could
// never succeed - so a sub-1 rate throttles rather than locking the key out); and a
// non-positive idleTTL or cleanupInterval falls back to DefaultIdleTTL / DefaultCleanupInterval.
// A non-positive maxKeys leaves the set unbounded, relying on idle eviction alone; a caller
// keyed on attacker-supplied input should pass a positive cap.
//
// On a bounded set at most maxKeys/2 keys may carry a quota override at once; a SetRate past that
// is dropped, leaving the key on the default quota (see SetRate). This reserves a non-overridden
// floor so the default limit cannot be driven off by throttling many keys.
//
// now is the clock the set reads time from; a nil now selects time.Now. It is fixed here, before
// the eviction goroutine starts, so the clock is never written while that goroutine reads it.
// Tests pass a manually advanced clock; production callers pass nil.
//
// Call Stop when the set is no longer needed to release its eviction goroutine.
func NewBucketSet(rate, burst float64, idleTTL, cleanupInterval time.Duration, maxKeys int, now func() time.Time) *BucketSet {
	if now == nil {
		now = time.Now
	}
	// A non-finite rate (NaN from a 0/0 derived limit, or an infinity) is not a usable refill
	// speed: left as-is it slips past the rate<=0 "unmetered" checks yet fails every rate>0 test,
	// so the set would start its eviction goroutine while Metered reported unmetered and Take never
	// denied - a rate limiter silently failing open. Collapse it to the canonical unmetered value
	// so the whole set is consistently unmetered and Metered says so.
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		rate = 0
	}
	s := &BucketSet{
		rate:    rate,
		burst:   floorBurst(burst, rate),
		idleTTL: idleTTL,
		maxKeys: maxKeys,
		now:     now,
		buckets: make(map[string]*bucket),
		stopped: make(chan struct{}),
	}
	if maxKeys > 0 {
		// Reserve at least half the slots for non-overridden buckets so evictOneForRoom has a
		// candidate to evict whenever one has refilled. Integer division keeps maxOverrides <=
		// maxKeys-1 for every maxKeys >= 1.
		s.maxOverrides = maxKeys / 2
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
	// throttled key a fresh budget. Guard the float->Duration conversion: for an extreme
	// burst/rate the nanosecond product overflows int64 and would wrap to a negative Duration,
	// silently skipping the clamp; cap it at the maximum Duration (an effectively unbounded TTL)
	// instead, which still upholds the invariant.
	if refillSeconds := s.burst / s.rate; refillSeconds > 0 {
		refill := time.Duration(math.MaxInt64)
		if refillSeconds < float64(math.MaxInt64)/float64(time.Second) {
			refill = time.Duration(refillSeconds * float64(time.Second))
		}
		if s.idleTTL < refill {
			s.idleTTL = refill
		}
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
	if b == nil || b.tokens < 1 {
		// A nil bucket means the set is at capacity and no slot could be reclaimed; deny rather
		// than serve an unmetered request (see bucketFor).
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
// retroactively either, the bucket simply refills faster from where it is. On a bounded set an
// override is kept until ClearRate is called, so idle eviction cannot restore the default quota
// behind the caller's back; on an unbounded set an override idle for idleTTL is reclaimed by the
// idle sweep instead, since there is no count cap to bound overrides otherwise (see evictIdle).
//
// A non-positive rate is ignored: an unmetered exception for a single key would be a
// footgun, and the caller that wants one can stop consulting the set for that key.
//
// SetRate on an unmetered set (one built with a non-positive rate) is also ignored. Take
// short-circuits on such a set and never consults its buckets, so an override would enforce
// nothing while leaking one permanent bucket per key - no eviction goroutine runs at all on an
// unmetered set, so nothing would ever reclaim it. A caller that means to throttle must build a
// metered set; Metered reports which it has.
//
// On a bounded set the number of overrides is capped at maxKeys/2 (see maxOverrides). Once that
// budget is spent, a SetRate that would add a new override - a fresh key, or an existing key not
// already overridden - is dropped: the key keeps the set's default quota rather than the reduced
// one. Dropping it is the lesser evil next to letting overrides fill every slot, which would both
// grow memory without bound (overridden buckets are exempt from idle eviction) and leave
// bucketFor no bucket to evict, quietly turning the default limit off for every other principal.
// The budget is spent only once maxKeys/2 distinct keys are being throttled at once. Note that a
// set built with maxKeys == 1 has a budget of zero, so it can carry no override at all: a single
// slot cannot both hold an override and leave a non-overridden bucket for eviction.
//
// SetRate reports whether the override took effect. It returns false when the set is unmetered,
// when rate is non-positive, or when the override budget is spent - cases where the key stays on
// the default quota - so a caller can distinguish "this principal is now throttled" from
// "the throttle was dropped" and log or alert accordingly.
func (s *BucketSet) SetRate(key string, rate, burst float64) bool {
	// An unmetered set never consults its buckets, so an override here would store state that is
	// enforced by nothing and reclaimed by nothing. s.rate is fixed at construction, so this is
	// safe to read without the lock, as Take does.
	if !s.Metered() {
		return false
	}
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 1) {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if b, ok := s.buckets[key]; ok && b.overridden {
		// Already overridden: update its quota in place, spending no further budget.
		s.refill(b, s.now())
		b.rate = rate
		b.burst = floorBurst(burst, rate)
		b.tokens = math.Min(b.tokens, b.burst)

		return true
	}

	if !s.overrideBudgetAvailable() {
		return false
	}

	b := s.bucketFor(key)
	if b == nil {
		// No slot could be reclaimed: the override budget keeps a non-overridden bucket present,
		// but eviction now also requires that bucket to have refilled to capacity, so a set whose
		// non-overridden buckets are all mid-refill has no victim. The new override is dropped and
		// the key keeps the default quota - the same fail-closed outcome SetRate reports for a
		// spent budget - rather than growing the set past the cap.
		return false
	}
	b.rate = rate
	b.burst = floorBurst(burst, rate)
	b.overridden = true
	b.tokens = math.Min(b.tokens, b.burst)
	s.overrides++

	return true
}

// overrideBudgetAvailable reports whether another bucket may be given a quota override without
// exceeding maxOverrides. An unbounded set (non-positive maxKeys) caps overrides by count nowhere;
// its idle sweep instead reclaims overrides by time (see evictIdle). Callers must hold s.mu.
func (s *BucketSet) overrideBudgetAvailable() bool {
	return s.maxKeys <= 0 || s.overrides < s.maxOverrides
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
	if b.overridden {
		s.overrides--
	}
	// Credit the time elapsed under the override at the override rate and advance b.last to now
	// before switching to the default rate. Without this the elapsed override period is later
	// refilled at the (higher) default rate on the next Take, letting a just-cleared key jump
	// straight to a full default bucket - the very thing this method is documented not to do.
	s.refill(b, s.now())
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

	if b, ok := s.buckets[key]; ok && b.overridden {
		s.overrides--
	}
	delete(s.buckets, key)
}

// Len returns the number of buckets currently held. It is exported for tests and for
// gauges reporting how much state the set has accumulated.
func (s *BucketSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.buckets)
}

// Stop terminates the eviction goroutine. Buckets are left in place, so a set that is still
// consulted after Stop keeps enforcing its limits; it simply stops reclaiming the memory of
// idle keys. Stop is idempotent.
func (s *BucketSet) Stop() {
	s.stopOnce.Do(func() { close(s.stopped) })
}

// refill brings b up to date for the time elapsed since its last update, never past its
// capacity. Callers must hold s.mu.
func (s *BucketSet) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(b.burst, b.tokens+elapsed.Seconds()*b.rate)
		b.last = now
	}
}

// bucketFor returns key's bucket, creating it full when the key is new and refilling it for the
// time elapsed since its last update. It serves both the metering path (Take) and the quota
// override write (SetRate): a new key at maxKeys evicts the least-recently-used non-overridden
// bucket to make room. It returns nil when the set is at maxKeys and no slot could be reclaimed,
// so the caller can fail closed. Callers must hold s.mu.
func (s *BucketSet) bucketFor(key string) *bucket {
	now := s.now()
	b, ok := s.buckets[key]
	if !ok {
		if s.maxKeys > 0 && len(s.buckets) >= s.maxKeys && !s.evictOneForRoom(now) {
			// No slot could be reclaimed: every bucket is either overridden or has not yet
			// refilled to capacity (evictOneForRoom evicts only full ones, so that recreating a
			// key never hands a still-throttled principal a fresh budget). Rather than grow past
			// the cap or hand back an unmetered bucket, return nil so the caller denies the
			// request - under resource exhaustion a rate limiter must fail closed, not open. An
			// attacker interleaving junk keys to force out a throttled one is therefore denied,
			// not served, until the junk keys' own buckets refill and become evictable.
			return nil
		}
		// A key not seen recently starts with a full bucket at the set's defaults.
		b = &bucket{tokens: s.burst, last: now, rate: s.rate, burst: s.burst}
		s.buckets[key] = b

		return b
	}

	s.refill(b, now)

	return b
}

// evictOneForRoom deletes the least-recently-used bucket that is both without a quota override and
// already refilled to capacity, making room for a new key when the set is at maxKeys, and reports
// whether it removed one. Callers must hold s.mu and pass the current time as now.
//
// Two kinds of bucket are never evicted:
//
//   - Overridden buckets carry a reduced quota for a key that is still being throttled, and
//     evicting one would reset it to a full default bucket.
//   - A bucket that has not refilled to capacity is still metering its key: recreating that key
//     (bucketFor seeds a new bucket full) would hand a throttled principal a fresh budget, which
//     is exactly the bypass an attacker gets by interleaving junk keys to force a throttled key
//     out between bursts. Evicting only full buckets closes that path - a full bucket has already
//     refilled to the budget a recreated bucket would be given, so dropping and recreating it is
//     free. This mirrors evictIdle, which evicts only buckets idle long enough to have refilled.
//
// When no bucket qualifies nothing is evicted and false is returned, so the caller fails closed.
func (s *BucketSet) evictOneForRoom(now time.Time) bool {
	var oldestKey string
	var oldest time.Time
	found := false
	for key, b := range s.buckets {
		if b.overridden || !s.refilledToCapacity(b, now) {
			continue
		}
		if !found || b.last.Before(oldest) {
			oldestKey, oldest, found = key, b.last, true
		}
	}

	if !found {
		return false
	}
	delete(s.buckets, oldestKey)

	return true
}

// refilledToCapacity reports whether b would be at its full burst as of now, without mutating it.
// It is the eviction guard's test for "dropping this bucket loses no budget": a bucket at capacity
// holds exactly what a freshly created one would. Callers must hold s.mu.
func (s *BucketSet) refilledToCapacity(b *bucket, now time.Time) bool {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return b.tokens >= b.burst
	}

	return b.tokens+elapsed.Seconds()*b.rate >= b.burst
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

// evictIdle drops the buckets of keys that have made no request within idleTTL. Such a bucket has
// already refilled to capacity, so dropping it loses no accounting.
//
// Whether an idle override is reclaimed depends on how the set bounds its overrides:
//
//   - On a bounded set, overrides are capped by count (maxOverrides) and cannot leak, so idle
//     overrides are kept as a safety net. The caller is expected to call ClearRate before
//     retiring a principal; if it does not, the bucket is retained rather than silently restoring
//     the default quota for a key that may still be throttled. This preserves the invariant that
//     a reduced quota is never restored behind the caller's back.
//   - On an unbounded set (non-positive maxKeys), overrides are bounded by nothing: evictOneForRoom
//     never runs, so without this an attacker rotating identities - one override minted per
//     identity, each then abandoned - leaks one permanent bucket per key. There, an override idle
//     for idleTTL is reclaimed too: a principal silent that long is treated as gone, and the
//     override de-escalates. Time-based reclamation is the only count-free bound available, and an
//     idle principal returning to a full default bucket is the accepted cost of it.
func (s *BucketSet) evictIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// On an unbounded set the idle sweep is the only reclamation path, so it must reach overrides
	// too; a bounded set bounds them by count and keeps them as a safety net.
	reclaimOverrides := s.maxKeys <= 0
	cutoff := s.now().Add(-s.idleTTL)
	for key, b := range s.buckets {
		if b.overridden && !reclaimOverrides {
			continue
		}
		if b.last.Before(cutoff) {
			if b.overridden {
				s.overrides--
			}
			delete(s.buckets, key)
		}
	}
}

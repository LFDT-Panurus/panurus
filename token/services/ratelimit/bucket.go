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
// allocation, evicting the least-recently-used non-overridden bucket in O(1) via an intrusive
// doubly-linked LRU list (see bucketFor and evictOneForRoom).
//
// Evicting a partially drained bucket under cap pressure must neither lock out legitimate new or
// established keys nor let an attacker reset a drained key's quota to a full burst by cycling
// through maxKeys throwaway keys:
//
//   - Rather than failing closed when existing buckets are mid-refill (which would let an attacker
//     drain maxKeys junk keys by one token each and deny every new key, while making established
//     keys with full buckets the preferred eviction victims), evictOneForRoom always evicts the
//     least-recently-used non-overridden bucket in O(1).
//   - Whenever an evicted victim holds v tokens (0 <= v <= burst), the newly created bucket
//     inherits that refilled balance v instead of being seeded unconditionally with a fresh
//     s.burst. If the victim was completely drained (v < 1), the new key is admitted into the map
//     with v tokens so its current Take returns false (preventing a key-cycling attacker from
//     minting free tokens), while subsequent requests on that now-resident key accrue tokens at
//     s.rate instead of being permanently locked out.
//
// On a bounded set, overridden buckets are kept out of the non-overridden LRU list so ordinary
// traffic cannot evict an active throttle override. maxOverrides caps overrides at half of maxKeys
// so at least half the slots remain non-overridden and evictOneForRoom always has a victim.
// Overridden buckets are reclaimed once they have been idle for at least idleTTL AND have refilled
// to their full default burst (or when SetRate needs a slot and the oldest overridden bucket has
// already been idle for idleTTL and refilled to its default burst), so abandoned overrides never
// permanently exhaust the override budget, while an active slow override can never be reset early
// to a full default bucket by backing off for idleTTL.
//
// A BucketSet is safe for concurrent use by multiple goroutines.
type BucketSet struct {
	// rate is the default refill speed in tokens per second. When it is not positive the
	// set is unmetered and Take always succeeds.
	rate float64
	// burst is the default bucket capacity in tokens.
	burst float64
	// idleTTL is the minimum idle duration before a bucket is eligible for idle eviction.
	// Note that a bucket is also only idle-evicted once its balance would have refilled to
	// s.burst at its active rate (see evictIdle), so configuring a sub-1 rate or a slow
	// override delays eviction until the bucket has fully refilled rather than silently
	// inflating idleTTL for every key.
	idleTTL time.Duration
	// maxKeys caps the number of buckets held simultaneously. When it is not positive the set
	// is unbounded and relies on idle eviction alone.
	maxKeys int
	// maxOverrides caps how many buckets may carry a quota override at once, at maxKeys/2, so that
	// at least half the slots stay non-overridden and evictOneForRoom always has a candidate to
	// evict. It is zero and unused for an unbounded set (maxKeys not positive).
	maxOverrides int
	// now is the clock, indirected for tests. It is set once by NewBucketSet before the eviction
	// goroutine starts and never written again, so it needs no lock despite being read under s.mu.
	now func() time.Time

	// mu guards buckets, overrides, the LRU list, and the state of each bucket. A single mutex is
	// enough: every operation is O(1) map and pointer manipulation.
	mu sync.Mutex
	// overrides is the number of buckets currently carrying a quota override, kept in step with
	// the overridden flag so SetRate can honour maxOverrides without scanning the map.
	overrides int
	buckets   map[string]*bucket

	// lruHead and lruTail are sentinels bracketing a doubly-linked list of the non-overridden
	// buckets, most-recently-used first: lruHead.next is the MRU bucket and lruTail.prev the LRU.
	// It makes evictOneForRoom pick a victim in O(1) instead of scanning every bucket under s.mu.
	lruHead, lruTail *bucket

	// overrideHead and overrideTail are sentinels bracketing a doubly-linked list of the
	// overridden buckets, most-recently-used first, so SetRate can reclaim an abandoned override in
	// O(1) from overrideTail.prev when overrides == maxOverrides.
	overrideHead, overrideTail *bucket

	// minEvictedTokens and minEvictedAt track the floor balance across all buckets evicted from the
	// set while still below s.burst. Whenever evictOneForRoom evicts a non-overridden bucket whose
	// refilled balance is below s.burst, the floor drops to min(floor, victim.tokens) and refills
	// forward at s.rate up to s.burst. Any newly created bucket on a capped set starts at most at
	// this floor, so an attacker who drains key A (0 tokens), forces A out with maxKeys throwaway
	// keys (even if each throwaway key only takes 1 token, leaving burst-1 in its slot), and then
	// re-enters as A can never receive more than the tokens A had genuinely accrued since its
	// eviction.
	minEvictedTokens float64
	minEvictedAt     time.Time

	stopOnce sync.Once
	stopped  chan struct{}
}

// bucket is one key's token bucket. tokens is the balance as of last. When overridden is
// set, rate and burst replace the set's defaults for this key alone.
type bucket struct {
	tokens     float64
	last       time.Time
	rate       float64
	burst      float64
	overridden bool

	// key is the map key this bucket is stored under, carried on the bucket so the LRU list can
	// delete it from the map in O(1) when it evicts the bucket from the tail.
	key string
	// prev and next link the bucket into either the non-overridden LRU list (lruHead..lruTail) or
	// the overridden LRU list (overrideHead..overrideTail).
	prev, next *bucket
}

// listRemove unlinks b from whichever doubly-linked list it is currently in, and is a no-op if b
// is unlinked. Callers must hold s.mu.
func listRemove(b *bucket) {
	if b.prev == nil {
		return
	}
	b.prev.next = b.next
	b.next.prev = b.prev
	b.prev = nil
	b.next = nil
}

// listPushFront links b right after head (the most-recently-used position). b must be unlinked.
// Callers must hold s.mu.
func listPushFront(head, b *bucket) {
	b.prev = head
	b.next = head.next
	head.next.prev = b
	head.next = b
}

// listTouch moves b to the front of head's list, linking it if it was unlinked. Callers must hold
// s.mu.
func listTouch(head, b *bucket) {
	if head.next == b {
		return
	}
	listRemove(b)
	listPushFront(head, b)
}

// lruRemove unlinks b from its current list. Callers must hold s.mu.
func (s *BucketSet) lruRemove(b *bucket) {
	listRemove(b)
}

// lruTouch marks b as most-recently-used in the appropriate list (non-overridden or overridden).
// Callers must hold s.mu.
func (s *BucketSet) lruTouch(b *bucket) {
	if b.overridden {
		listTouch(s.overrideHead, b)

		return
	}
	listTouch(s.lruHead, b)
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
// Note on idleTTL and refill duration: idleTTL is floored to at least burst/rate so a default
// bucket is never idle-evicted before it has refilled to capacity. When rate < 1 (e.g. a per-hour
// or per-day quota), burst/rate is correspondingly long (1/rate seconds), so idle buckets are
// retained until they refill to one full token; callers using very low rates with untrusted keys
// should always configure a positive maxKeys cap to bound memory by count.
//
// On a bounded set at most maxKeys/2 keys may carry a quota override at once; a SetRate past that
// reclaims an idle, fully refilled override if one exists, or else is dropped, leaving the key on
// the default quota (see SetRate). This reserves a non-overridden floor so the default limit
// cannot be driven off by throttling many keys.
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
	b := floorBurst(burst, rate)
	s := &BucketSet{
		rate:             rate,
		burst:            b,
		idleTTL:          idleTTL,
		maxKeys:          maxKeys,
		now:              now,
		buckets:          make(map[string]*bucket),
		minEvictedTokens: b,
		minEvictedAt:     now(),
		stopped:          make(chan struct{}),
	}
	s.lruHead = &bucket{}
	s.lruTail = &bucket{}
	s.lruHead.next = s.lruTail
	s.lruTail.prev = s.lruHead

	s.overrideHead = &bucket{}
	s.overrideTail = &bucket{}
	s.overrideHead.next = s.overrideTail
	s.overrideTail.prev = s.overrideHead

	if maxKeys > 0 {
		// Reserve at least half the slots for non-overridden buckets so evictOneForRoom always has
		// a candidate to evict. Integer division keeps maxOverrides <= maxKeys-1 for every
		// maxKeys >= 1.
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
// retroactively either, the bucket simply refills faster from where it is. An override is
// exempt from ordinary cap eviction and is only reclaimed by idle eviction once it has both
// been idle for at least idleTTL and refilled to the full default burst (s.burst) at its
// override rate, so backing off for idleTTL can never reset a slow override to a full default
// bucket early, while abandoned overrides are still eventually reclaimed on both bounded and
// unbounded sets.
//
// A non-positive rate is ignored: an unmetered exception for a single key would be a
// footgun, and the caller that wants one can stop consulting the set for that key.
//
// SetRate on an unmetered set (one built with a non-positive rate) is also ignored. Take
// short-circuits on such a set and never consults its buckets, so an override would enforce
// nothing while leaking one permanent bucket per key.
//
// On a bounded set the number of overrides is capped at maxKeys/2 (see maxOverrides). Once that
// budget is spent, SetRate first checks whether the least-recently-used override has been idle for
// idleTTL and refilled to s.burst; if not, a SetRate that would add a new override is dropped and
// the key keeps the set's default quota.
//
// SetRate reports whether the override took effect.
func (s *BucketSet) SetRate(key string, rate, burst float64) bool {
	if !s.Metered() {
		return false
	}
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 1) {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if b, ok := s.buckets[key]; ok && b.overridden {
		// Already overridden: update its quota in place, spending no further budget.
		s.refill(b, now)
		b.rate = rate
		b.burst = floorBurst(burst, rate)
		b.tokens = math.Min(b.tokens, b.burst)
		s.lruTouch(b)

		return true
	}

	if !s.overrideBudgetAvailable(now) {
		return false
	}

	b := s.bucketForAt(key, now)
	if b == nil {
		return false
	}
	b.rate = rate
	b.burst = floorBurst(burst, rate)
	b.overridden = true
	b.tokens = math.Min(b.tokens, b.burst)
	// Move from the non-overridden LRU list to the overridden LRU list.
	s.lruTouch(b)
	s.overrides++

	return true
}

// overrideBudgetAvailable reports whether another bucket may be given a quota override without
// exceeding maxOverrides, reclaiming the least-recently-used override if it has already been idle
// for idleTTL and refilled to the full default burst. Callers must hold s.mu.
func (s *BucketSet) overrideBudgetAvailable(now time.Time) bool {
	if s.maxKeys <= 0 {
		return true
	}
	if s.overrides < s.maxOverrides {
		return true
	}
	if s.maxOverrides <= 0 {
		return false
	}
	// Check whether the oldest overridden bucket is idle and has fully refilled to the default
	// burst; if so, reclaiming it frees an override slot without forgiving an active throttle.
	oldest := s.overrideTail.prev
	if oldest != s.overrideHead && s.canReclaimIdleBucket(oldest, now, now.Add(-s.idleTTL)) {
		s.lruRemove(oldest)
		delete(s.buckets, oldest.key)
		s.overrides--

		return true
	}

	return false
}

// ClearRate drops key's quota override, returning it to the set's defaults and making it
// eligible for normal eviction again. The balance is kept, and clamped to the default
// capacity: a key coming back from a reduced quota refills towards the default rather than
// jumping straight to a full default bucket.
func (s *BucketSet) ClearRate(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[key]
	if !ok {
		return
	}
	// Settle the balance at the current (reduced) rate and advance b.last to now before swapping
	// in the defaults, exactly as bucketForOverride and the in-place SetRate branch do. Otherwise
	// the time the key sat idle under the override is left uncredited with b.last stale, and the
	// next Take refills that whole gap at the new, higher default rate — handing back a full
	// default bucket, the jump this method's contract says it avoids.
	s.refill(b, s.now())
	if b.overridden {
		s.overrides--
	}
	b.rate = s.rate
	b.burst = s.burst
	b.overridden = false
	b.tokens = math.Min(b.tokens, s.burst)
	// Move from the overridden list back to the non-overridden LRU list.
	s.lruTouch(b)
}

// Reset discards key's bucket, including any quota override, so its next Take starts from a
// full default bucket. It is meant for tests and for administrative "forgive this key"
// actions, not for the metering path.
func (s *BucketSet) Reset(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[key]
	if !ok {
		return
	}
	if b.overridden {
		s.overrides--
	}
	s.lruRemove(b)
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

// bucketFor returns key's bucket at the current clock time. Callers must hold s.mu.
func (s *BucketSet) bucketFor(key string) *bucket {
	return s.bucketForAt(key, s.now())
}

// bucketForAt returns key's bucket as of now, creating it when the key is new and refilling it for
// the time elapsed since its last update. When the set is at maxKeys, evictOneForRoom evicts the
// least-recently-used non-overridden bucket in O(1) and transfers its refilled token balance to
// the newly created slot so that cap eviction neither locks out new keys nor grants a fresh burst
// to a key-cycling attacker. Callers must hold s.mu.
func (s *BucketSet) bucketForAt(key string, now time.Time) *bucket {
	b, ok := s.buckets[key]
	if !ok {
		initialTokens := s.burst
		if s.maxKeys > 0 && len(s.buckets) >= s.maxKeys {
			var okEvict bool
			initialTokens, okEvict = s.evictOneForRoom(now)
			if !okEvict {
				return nil
			}
		}
		b = &bucket{
			tokens: initialTokens,
			last:   now,
			rate:   s.rate,
			burst:  s.burst,
			key:    key,
		}
		s.buckets[key] = b
		s.lruTouch(b)

		return b
	}

	s.refill(b, now)
	s.lruTouch(b)

	return b
}

// evictOneForRoom evicts the least-recently-used non-overridden bucket in O(1) from the tail of
// the intrusive LRU list and returns the refilled token balance (0 <= v <= s.burst) for the
// incoming bucket.
//
// When the evicted victim holds v tokens (0 <= v <= s.burst), the new bucket inherits those v
// tokens and is inserted into the map at the MRU head:
//
//   - If an attacker takes 1 token from maxKeys throwaway keys, the LRU victim still holds
//     burst-1 tokens, so a new user is admitted and served immediately.
//   - If an attacker drains all maxKeys junk keys to 0, a new honest user is still admitted into
//     the map (evicting the oldest junk key) and begins accruing tokens at s.rate, so sustained
//     traffic for that user succeeds at the configured rate instead of being permanently denied.
//   - If an attacker cycles key A + maxKeys junk keys to force A out and re-enter as A, A inherits
//     at most the refilled balance of the evicted slot, never a fresh full burst.
//
// Callers must hold s.mu.
func (s *BucketSet) evictOneForRoom(now time.Time) (float64, bool) {
	lru := s.lruTail.prev
	if lru == s.lruHead {
		return 0, false
	}
	s.refill(lru, now)
	victimTokens := math.Min(s.burst, lru.tokens)
	if elapsed := now.Sub(s.minEvictedAt); elapsed > 0 {
		s.minEvictedTokens = math.Min(s.burst, s.minEvictedTokens+elapsed.Seconds()*s.rate)
		s.minEvictedAt = now
	}
	if victimTokens < s.minEvictedTokens {
		s.minEvictedTokens = victimTokens
		s.minEvictedAt = now
	}
	s.lruRemove(lru)
	delete(s.buckets, lru.key)

	return s.minEvictedTokens, true
}

// canReclaimIdleBucket reports whether b has been idle since at least cutoff AND has refilled to
// at least the full default burst (s.burst) as of now.
//
// Requiring the bucket to have refilled to s.burst before idle eviction ensures that an overridden
// bucket with a slow refill rate (e.g. rate=0.001, burst=1 on a set with default burst=100 and
// idleTTL=10m) is never deleted after 10m only for the next Take to recreate it with 100 default
// tokens. Once elapsed * b.rate + b.tokens >= s.burst, the key would have accrued a full default
// bucket even at its reduced override rate, so reclaiming the bucket cannot grant the principal
// any extra tokens. Callers must hold s.mu.
func (s *BucketSet) canReclaimIdleBucket(b *bucket, now, cutoff time.Time) bool {
	if !b.last.Before(cutoff) {
		return false
	}
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return b.tokens >= math.Max(b.burst, s.burst)
	}
	target := math.Max(b.burst, s.burst)

	return b.tokens+elapsed.Seconds()*b.rate >= target
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

// evictIdle drops the buckets of keys that have made no request within idleTTL and whose balance
// has refilled to at least the full default burst (s.burst).
//
// Reclaiming idle overrides on both bounded and unbounded sets once they have been idle for
// idleTTL AND accrued at least s.burst tokens at their override rate guarantees two invariants:
//
//  1. An active throttled key can never escape its reduced quota by backing off for idleTTL
//     between bursts: if its override rate would not yet have refilled s.burst tokens, the
//     bucket stays in place.
//  2. Abandoned overrides (from principals that were throttled and never returned) do not hold
//     their override slot forever on a bounded set or leak memory on an unbounded set.
func (s *BucketSet) evictIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	cutoff := now.Add(-s.idleTTL)
	for key, b := range s.buckets {
		if s.canReclaimIdleBucket(b, now, cutoff) {
			if b.overridden {
				s.overrides--
			}
			s.lruRemove(b)
			delete(s.buckets, key)
		}
	}
}

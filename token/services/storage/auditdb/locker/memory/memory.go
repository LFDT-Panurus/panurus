/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/dedup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"golang.org/x/sync/semaphore"
)

// Locker is the default in-memory Locker. It uses weighted semaphores
// (weight 1) so that AcquireLocks respects context cancellation and deadlines.
// Suitable for single-replica deployments.
//
// Enrollment-ID semaphores and per-anchor bookkeeping are held in two separate
// maps on purpose. Both are keyed by unconstrained strings of unrelated
// provenance — a request anchor and an identity's enrollment ID — so a single
// shared map would let one namespace's key resolve to the other's value type,
// and the type assertions on the way out would panic. Two maps make that
// impossible by construction rather than by convention.
type Locker struct {
	cfg Config

	// sems maps an enrollment ID to the weight-1 semaphore guarding it. Entries
	// are never removed: the semaphore *is* the identity of the lock, so
	// discarding one while a caller is blocked on it would hand the next caller a
	// different semaphore and let both believe they hold the ID.
	sems sync.Map

	// mu guards the anchors map itself. It is never held across a semaphore
	// acquisition, so it cannot be the lock a blocked caller is waiting behind.
	mu sync.Mutex
	// anchors maps a request anchor to its lock bookkeeping.
	anchors map[string]*anchorState
}

// anchorState is the bookkeeping for a single anchor. Its mutex serialises the
// whole of AcquireLocks and ReleaseLocks for that anchor, which is what makes
// the reconciliation in AcquireLocks atomic: the currently held set is read, the
// difference against the requested set is acquired, and the result is written
// back with no other call for the same anchor interleaving.
//
// The lock is per anchor rather than one lock for the whole Locker because
// AcquireLocks blocks on semaphores while holding it, and the permit it waits
// for is released by some *other* anchor's ReleaseLocks. Under a single shared
// lock that release would wait for the blocked acquisition, which waits for the
// release — a deadlock. Per-anchor locks also leave acquisitions for unrelated
// anchors fully concurrent, which is the common case.
type anchorState struct {
	mu sync.Mutex
	// refs counts the callers that have looked this state up and not yet finished
	// with it, so it cannot be evicted from under them.
	refs int
	// eIDs are the enrollment IDs currently held for the anchor, deduplicated and
	// sorted. Written only by the caller holding mu (or, once refs drops to zero,
	// by the last caller to let go of it).
	eIDs []string
}

// DefaultAcquireDeadline is the waiting budget a Locker uses when its Config
// leaves AcquireDeadline unset. It matches the Postgres backend's default so a
// deployment that switches backends does not silently change how long an audit
// can block on a contended enrollment ID.
const DefaultAcquireDeadline = time.Minute

// Config configures the in-memory Locker.
type Config struct {
	// AcquireDeadline bounds how long one AcquireLocks call waits for enrollment
	// IDs held by another anchor. The Locker contract requires implementations to
	// bound their own waiting: without a budget of its own this backend could only
	// ever stop when the caller's context did, so a caller with no deadline blocked
	// forever and no failure could be reported as ErrLockAcquireTimeout — the very
	// signal callers use to tell "already waited in full" from "worth retrying".
	AcquireDeadline time.Duration `yaml:"acquireDeadline"`
}

// withDefaults returns c with unset or nonsensical values replaced by defaults.
func (c Config) withDefaults() Config {
	if c.AcquireDeadline <= 0 {
		c.AcquireDeadline = DefaultAcquireDeadline
	}

	return c
}

// New returns an empty in-memory Locker with the default configuration.
func New() *Locker {
	return NewWithConfig(Config{})
}

// NewWithConfig returns an empty in-memory Locker ready for use.
func NewWithConfig(cfg Config) *Locker {
	return &Locker{cfg: cfg.withDefaults(), anchors: make(map[string]*anchorState)}
}

// AcquireLocks blocks until it holds the lock for every enrollment ID in eIDs,
// then records them under anchor for later release.
//
// Implementation: the enrollment IDs are deduplicated and sorted (see
// dedup.AndSort) so all callers acquire shared locks in the same order and
// cannot deadlock. For each ID it lazily creates a weight-1 semaphore in the
// sems map and acquires it; using semaphore.Acquire (rather than a plain Mutex)
// means a blocked acquisition still honours ctx cancellation/deadline. If any
// acquisition fails, the locks taken so far in this call are released and the
// error is returned, so the call is all-or-nothing — and anything the anchor
// already held from an earlier call is left untouched.
//
// The wait is bounded by cfg.AcquireDeadline as well as by ctx, so a caller with
// no deadline of its own still gets an answer, reported as ErrLockAcquireTimeout
// once that budget is spent.
//
// Re-acquiring under an anchor that is still live keeps the IDs it already holds
// and releases the ones dropped from the set, matching the distributed locker's
// lease-refresh behaviour. Without that reconciliation the previous list would
// simply be overwritten, permanently leaking the permits it recorded. The
// reconciliation runs under the anchor's lock because it is a read-modify-write
// of that record: performed lock-free, two concurrent re-acquisitions that both
// narrow the set read the same stale record, both conclude the same ID is now
// stale, and both release its permit — which panics the process, since releasing
// a semaphore more than it was acquired is a programming error in
// golang.org/x/sync/semaphore rather than a no-op.
//
// Adding IDs to a live anchor is refused with ErrLockSetWidened. The sorted order
// only covers the IDs taken within one call, so an anchor that keeps its existing
// permits while waiting for new ones is holding locks outside that order: two
// anchors widening into each other's IDs deadlock, and permanently, since this
// method holds the anchor's lock across the blocking acquisition and so blocks
// the ReleaseLocks that would break the cycle. Refusing it makes the cycle
// unconstructible rather than merely bounded.
func (m *Locker) AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error {
	deduped := dedup.AndSort(eIDs)

	st := m.lockAnchor(anchor)
	defer m.unlockAnchor(anchor, st)

	if len(deduped) == 0 {
		// Acquiring nothing succeeds, and must leave whatever the anchor already
		// holds in place: recording the empty set here would release those locks
		// while reporting success, so the caller would go on believing it still
		// held them while another anchor was free to take them. The distributed
		// locker returns early for the same reason.
		return nil
	}

	// Only the IDs the anchor does not already hold are acquired: re-acquiring one
	// this very caller holds would block on its own permit.
	added := dedup.Added(deduped, st.eIDs)
	if len(st.eIDs) > 0 && len(added) > 0 {
		return errors.Wrapf(errs.ErrLockSetWidened,
			"anchor [%s] holds %v and cannot also take %v", anchor, st.eIDs, added)
	}

	// The budget bounds the blocking acquisitions below, so a caller that passes no
	// deadline of its own still gets an answer.
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, m.cfg.AcquireDeadline)
	defer cancelAcquire()

	acquired := make([]string, 0, len(added))
	for _, id := range added {
		if err := m.acquireOne(acquireCtx, id); err != nil {
			m.releaseAll(acquired)

			return err
		}
		acquired = append(acquired, id)
	}

	// Commit the new set. Anything the anchor held but no longer needs would
	// otherwise be unreachable, since the caller's only handle on it was the
	// record just replaced.
	stale := dedup.Dropped(st.eIDs, deduped)
	st.eIDs = deduped
	m.releaseAll(stale)

	return nil
}

// ReleaseLocks releases every enrollment-ID lock previously acquired under
// anchor. It takes the anchor's recorded ID list, clears it, and releases each
// semaphore. It is a no-op if the anchor holds nothing (e.g. already released,
// or never acquired), so it is safe to call more than once and safe to defer.
func (m *Locker) ReleaseLocks(_ context.Context, anchor string) {
	st := m.lockAnchor(anchor)
	defer m.unlockAnchor(anchor, st)

	eIDs := st.eIDs
	st.eIDs = nil
	m.releaseAll(eIDs)
}

// AssertLocksHeld always succeeds for the in-memory locker: locks live in this
// process's memory and cannot be lost or stolen by another replica, so there is
// nothing to re-verify. It exists to satisfy the Locker interface, whose
// distributed implementations use it to detect a lost lease — and, per that
// interface's contract, an anchor holding no locks is not a failure either.
func (m *Locker) AssertLocksHeld(_ context.Context, _ string) error {
	return nil
}

// acquireOne takes the permit for a single enrollment ID.
//
// TryAcquire is attempted before the blocking Acquire so that a failure can be
// classified exactly: a permit that was already taken is contention, whereas one
// that was free means the caller had stopped waiting. A blocking Acquire on its
// own cannot tell those apart — it reports the context error either way — which
// is why a cancelled request used to be reported as a lock conflict. The two
// calls share one admission policy (both refuse to jump an existing queue of
// waiters), so probing first does not let this caller barge ahead.
func (m *Locker) acquireOne(ctx context.Context, eID string) error {
	sem := m.semaphoreFor(eID)
	if sem.TryAcquire(1) {
		if err := ctx.Err(); err != nil {
			// The ID was free but the caller is no longer waiting for it. Holding on
			// to the permit would strand it, since a caller that gets an error does
			// not go on to release anything.
			sem.Release(1)

			return acquireError(err, eID, false)
		}

		return nil
	}
	if err := sem.Acquire(ctx, 1); err != nil {
		return acquireError(err, eID, true)
	}

	return nil
}

// lockAnchor returns anchor's state with its lock held, creating the state on
// first use. The reference taken here keeps the state alive until the matching
// unlockAnchor, so a concurrent caller cannot evict it mid-use.
func (m *Locker) lockAnchor(anchor string) *anchorState {
	m.mu.Lock()
	st, ok := m.anchors[anchor]
	if !ok {
		st = &anchorState{}
		m.anchors[anchor] = st
	}
	st.refs++
	m.mu.Unlock()
	st.mu.Lock()

	return st
}

// unlockAnchor releases anchor's lock and drops the state from the map once it
// holds no enrollment IDs and no other caller references it. Anchors are request
// identifiers, so without the eviction the map would grow by one entry for every
// transaction the process ever audits.
func (m *Locker) unlockAnchor(anchor string, st *anchorState) {
	// Emptiness and the reference count are read together, under both locks, and
	// st.mu is only dropped afterwards. Sampling emptiness first and acting on it
	// after taking m.mu let a waiter run a whole acquisition in between: it was
	// already counted in refs, so it blocked on st.mu rather than creating its own
	// state, recorded its enrollment IDs, and released its reference — leaving this
	// caller to evict, on the strength of a now-stale flag, an anchor that held
	// permits. Nothing could reach those permits afterwards, since the next
	// ReleaseLocks for the anchor created a fresh state with no IDs recorded, so
	// every later audit touching them blocked until its own deadline, for the
	// lifetime of the process.
	//
	// Taking m.mu while holding st.mu is safe in this order: lockAnchor releases
	// m.mu before it waits on st.mu, so no caller ever holds m.mu while blocked on
	// an anchor's lock.
	m.mu.Lock()
	st.refs--
	if st.refs == 0 && len(st.eIDs) == 0 {
		delete(m.anchors, anchor)
	}
	m.mu.Unlock()
	st.mu.Unlock()
}

// semaphoreFor returns the weight-1 semaphore guarding eID, creating it on
// first use.
func (m *Locker) semaphoreFor(eID string) *semaphore.Weighted {
	boxed, _ := m.sems.LoadOrStore(eID, semaphore.NewWeighted(1))
	sem, ok := boxed.(*semaphore.Weighted)
	if !ok {
		// Unreachable: only this method writes to sems, and only semaphores.
		sem = semaphore.NewWeighted(1)
	}

	return sem
}

// releaseAll releases one permit for each enrollment ID in eIDs.
func (m *Locker) releaseAll(eIDs []string) {
	for _, id := range eIDs {
		boxed, ok := m.sems.Load(id)
		if !ok {
			continue
		}
		if sem, ok := boxed.(*semaphore.Weighted); ok {
			sem.Release(1)
		}
	}
}

// acquireError classifies a failed acquisition of eID. semaphore.Acquire only
// fails when ctx is done, which covers two situations the caller must be able to
// tell apart: the ID was held by someone else and this caller ran out of time
// waiting for it, or the caller had already given up on its own.
//
// contended says which. Only a genuine conflict attaches the shared sentinels,
// since ErrLockContention and ErrLockAcquireTimeout are what callers test to
// decide whether a lock conflict is worth retrying and whether the waiting
// budget is already spent. Attaching them unconditionally reported every
// cancellation — a node shutting down, a caller whose own deadline elapsed
// elsewhere — as a lock conflict, which is both wrong and a divergence from the
// distributed locker, which returns a plain context error in that case.
func acquireError(err error, eID string, contended bool) error {
	sentinels := make([]error, 0, 3)
	if contended {
		sentinels = append(sentinels, errs.ErrLockContention)
		if errors.Is(err, context.DeadlineExceeded) {
			// The deadline elapsed while waiting out another holder, so the whole
			// waiting budget went on this attempt and repeating it adds delay rather
			// than a fresh chance.
			sentinels = append(sentinels, errs.ErrLockAcquireTimeout)
		}
	}

	return errors.Wrapf(errors.Join(append(sentinels, err)...),
		"failed to acquire lock for enrollment ID [%s]", eID)
}

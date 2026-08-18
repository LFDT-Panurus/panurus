/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocker_AcquireAndRelease(t *testing.T) {
	l := memory.New()
	ctx := context.Background()

	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))
	l.ReleaseLocks(ctx, "anchor1")
}

func TestLocker_DeadlockPrevention(t *testing.T) {
	l := memory.New()
	ctx := context.Background()
	done := make(chan struct{})

	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = l.AcquireLocks(ctx, "a1", "alice", "bob")
			time.Sleep(5 * time.Millisecond)
			l.ReleaseLocks(ctx, "a1")
		}()
		go func() {
			defer wg.Done()
			_ = l.AcquireLocks(ctx, "a2", "bob", "alice")
			time.Sleep(5 * time.Millisecond)
			l.ReleaseLocks(ctx, "a2")
		}()
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock detected")
	}
}

// mustAcquireWithin fails the test if AcquireLocks has not returned within a
// short budget. Every acquisition in these tests is expected to be uncontended,
// so a hang means the locker is blocking on a permit it already holds.
func mustAcquireWithin(t *testing.T, l *memory.Locker, anchor string, eIDs ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, l.AcquireLocks(ctx, anchor, eIDs...))
}

// TestLocker_AnchorEqualToEnrollmentID covers the key-namespace collision from
// issue #2040. Anchors and enrollment IDs are unconstrained strings from
// unrelated sources, and both used to be stored in one sync.Map: an anchor equal
// to an enrollment ID made a later lookup return the other namespace's value
// type, and the unchecked type assertion on it panicked. Every sub-case here
// panicked, released the wrong thing, or deadlocked before the two maps were
// split.
func TestLocker_AnchorEqualToEnrollmentID(t *testing.T) {
	t.Run("anchor equals its own enrollment id", func(t *testing.T) {
		l := memory.New()
		mustAcquireWithin(t, l, "x", "x")
		l.ReleaseLocks(context.Background(), "x")

		// The enrollment ID must be free again, not stranded by the release.
		mustAcquireWithin(t, l, "other", "x")
	})

	t.Run("later anchor equals an earlier enrollment id", func(t *testing.T) {
		l := memory.New()
		ctx := context.Background()
		mustAcquireWithin(t, l, "a", "x")

		// "x" is a live semaphore key; using it as an anchor used to make the next
		// acquisition assert a []string as a *semaphore.Weighted and panic.
		mustAcquireWithin(t, l, "x", "y")

		l.ReleaseLocks(ctx, "a")
		l.ReleaseLocks(ctx, "x")
		mustAcquireWithin(t, l, "fresh", "x", "y")
	})

	t.Run("release of an anchor that is only an enrollment id", func(t *testing.T) {
		l := memory.New()
		ctx := context.Background()
		mustAcquireWithin(t, l, "a", "x")

		// "x" was never an anchor. This used to load the semaphore stored under
		// "x", assert it as []string and panic — after deleting it, stranding the
		// permit anchor "a" still held.
		l.ReleaseLocks(ctx, "x")

		l.ReleaseLocks(ctx, "a")
		mustAcquireWithin(t, l, "b", "x")
	})
}

// TestLocker_ReleaseUnknownAnchor documents that releasing something never
// acquired is a silent no-op, so callers can defer Release unconditionally.
func TestLocker_ReleaseUnknownAnchor(t *testing.T) {
	l := memory.New()
	ctx := context.Background()

	l.ReleaseLocks(ctx, "never-acquired")
	mustAcquireWithin(t, l, "never-acquired", "alice")
	l.ReleaseLocks(ctx, "never-acquired")
	l.ReleaseLocks(ctx, "never-acquired")
}

// TestLocker_ReacquireSameAnchor covers the permit leak that shared the same
// root cause as the collision: AcquireLocks overwrote the anchor's recorded ID
// list, so any ID dropped from it could never be released again — the caller's
// only handle on it was the list just discarded. Re-acquiring the same list also
// used to deadlock against the caller's own permits.
func TestLocker_ReacquireSameAnchor(t *testing.T) {
	t.Run("same set is idempotent", func(t *testing.T) {
		l := memory.New()
		mustAcquireWithin(t, l, "a", "alice", "bob")
		mustAcquireWithin(t, l, "a", "alice", "bob")

		l.ReleaseLocks(context.Background(), "a")
		mustAcquireWithin(t, l, "b", "alice", "bob")
	})

	t.Run("dropped enrollment ids are released", func(t *testing.T) {
		l := memory.New()
		mustAcquireWithin(t, l, "a", "alice", "bob")
		mustAcquireWithin(t, l, "a", "bob")

		// "alice" is no longer recorded under "a", so it must have been released
		// rather than leaked.
		mustAcquireWithin(t, l, "other", "alice")

		l.ReleaseLocks(context.Background(), "a")
		mustAcquireWithin(t, l, "another", "bob")
	})

	t.Run("widening a live anchor is refused", func(t *testing.T) {
		l := memory.New()
		ctx := context.Background()
		mustAcquireWithin(t, l, "a", "alice")

		err := l.AcquireLocks(ctx, "a", "alice", "bob")
		require.ErrorIs(t, err, errs.ErrLockSetWidened)

		// The refusal changed nothing: "alice" is still held under "a", and "bob" was
		// never reached for.
		mustAcquireWithin(t, l, "holder-bob", "bob")
		l.ReleaseLocks(ctx, "a")
		mustAcquireWithin(t, l, "holder-alice", "alice")
	})
}

// TestLocker_WideningLiveAnchorsCannotDeadlock is the regression test for the
// cross-anchor cycle. dedup.AndSort only orders the enrollment IDs taken within
// one call, so an anchor that keeps its earlier permits while waiting for new ones
// holds locks outside that order. Two anchors widening into each other's IDs then
// wait on each other — and permanently, because AcquireLocks holds the anchor's
// lock across the blocking acquisition, which blocks the ReleaseLocks that would
// break the cycle. Before widening was refused, neither call below returned.
func TestLocker_WideningLiveAnchorsCannotDeadlock(t *testing.T) {
	l := memory.New()
	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "a", "alice"))
	require.NoError(t, l.AcquireLocks(ctx, "b", "bob"))

	done := make(chan error, 2)
	go func() { done <- l.AcquireLocks(ctx, "a", "alice", "bob") }()
	go func() { done <- l.AcquireLocks(ctx, "b", "alice", "bob") }()

	for range 2 {
		select {
		case err := <-done:
			require.ErrorIs(t, err, errs.ErrLockSetWidened)
		case <-time.After(5 * time.Second):
			t.Fatal("deadlock detected: two anchors widening into each other's enrollment IDs")
		}
	}

	// Both anchors kept exactly what they held, so neither ID is free and neither is
	// stranded.
	l.ReleaseLocks(ctx, "a")
	l.ReleaseLocks(ctx, "b")
	mustAcquireWithin(t, l, "c", "alice", "bob")
}

// TestLocker_BoundsItsOwnWait covers the waiting budget the Locker contract
// requires every implementation to have. This backend had none: a blocked
// acquisition ended only when the caller's context did, so a caller with no
// deadline waited forever, and no failure could ever carry ErrLockAcquireTimeout —
// the signal auditor.Service uses to tell "already waited in full" from "worth
// another attempt". The Postgres backend bounded itself with acquireDeadline, so
// this was a divergence between two deployments of the same code.
func TestLocker_BoundsItsOwnWait(t *testing.T) {
	l := memory.NewWithConfig(memory.Config{AcquireDeadline: 150 * time.Millisecond})
	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "holder", "alice"))

	done := make(chan error, 1)
	// Deliberately no deadline on the caller's context: the budget under test is the
	// locker's own.
	go func() { done <- l.AcquireLocks(ctx, "waiter", "alice") }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, errs.ErrLockContention)
		require.ErrorIs(t, err, errs.ErrLockAcquireTimeout,
			"spending the whole budget must be distinguishable from plain contention")
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireLocks did not bound its own wait for a caller with no deadline")
	}

	require.NoError(t, l.AssertLocksHeld(ctx, "holder"), "the holder keeps its lock")
}

// TestLocker_EvictionDoesNotStrandPermits is the regression test for the stale
// emptiness flag in unlockAnchor. It read whether the anchor still held anything
// before releasing the anchor's lock, then acted on that read after taking the map
// lock — leaving room for a waiter to run a whole acquisition in between, record
// its enrollment IDs and drop its reference, so this caller evicted an anchor that
// did hold permits. Nothing could reach them afterwards: the next ReleaseLocks for
// the anchor built a fresh state with no IDs recorded and released nothing, so
// every later audit touching those IDs blocked until its own deadline for the
// lifetime of the process.
//
// The window is small, so the interleaving has to be driven repeatedly. Each round
// checks that "x" is still acquirable by another anchor once the round's holder has
// let it go.
func TestLocker_EvictionDoesNotStrandPermits(t *testing.T) {
	const rounds = 20000
	l := memory.New()
	ctx := context.Background()

	for i := range rounds {
		require.NoError(t, l.AcquireLocks(ctx, "a", "x"))

		var wg sync.WaitGroup
		wg.Add(2)
		// One caller releases the anchor while another re-acquires it, so the release
		// computes emptiness for a state the acquisition is about to fill.
		go func() { defer wg.Done(); l.ReleaseLocks(ctx, "a") }()
		go func() { defer wg.Done(); _ = l.AcquireLocks(ctx, "a", "x") }()
		wg.Wait()

		l.ReleaseLocks(ctx, "a")

		// "x" is held by nobody now, so a fresh anchor must be able to take it
		// immediately. A stranded permit shows up here as a timeout.
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := l.AcquireLocks(probe, "probe", "x")
		cancel()
		require.NoError(t, err, "enrollment id [x] was stranded after %d round(s)", i+1)
		l.ReleaseLocks(ctx, "probe")
	}
}

// TestLocker_ConcurrentReacquireDoesNotOverRelease is the regression test for the
// reconciliation race. Narrowing an anchor's enrollment-ID set means releasing the
// IDs it dropped, and computing that from a lock-free read of the anchor's record
// let concurrent callers all see the same pre-narrowing set: every one of them
// concluded the same ID was now stale and released its permit. Releasing a
// semaphore more times than it was acquired panics
// (`semaphore: released more than held`), which takes the auditor process down.
//
// The pre-fix code survives a single-threaded narrowing, so the race has to be
// driven concurrently and repeatedly to show up.
func TestLocker_ConcurrentReacquireDoesNotOverRelease(t *testing.T) {
	const (
		rounds  = 200
		callers = 4
	)
	for range rounds {
		l := memory.New()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, l.AcquireLocks(ctx, "a", "x", "y"))

		var wg sync.WaitGroup
		wg.Add(callers)
		for range callers {
			go func() {
				defer wg.Done()
				// {x, y} narrowed to {x}: "y" is stale for whichever caller commits
				// first, and must be released exactly once no matter how many callers
				// computed that it was stale.
				_ = l.AcquireLocks(ctx, "a", "x")
			}()
		}
		wg.Wait()
		cancel()

		// "y" was released exactly once, so exactly one other anchor can take it,
		// while "x" is still held by "a".
		fresh := context.Background()
		require.NoError(t, l.AcquireLocks(fresh, "holder-y", "y"))
		l.ReleaseLocks(fresh, "a")
		require.NoError(t, l.AcquireLocks(fresh, "holder-x", "x"))
	}
}

// TestLocker_EmptyReacquireKeepsHeldLocks covers the other half of the
// reconciliation contract. Acquiring an empty set is a successful acquisition of
// nothing, so it must leave the anchor's existing locks alone; recording the empty
// set instead released them while returning nil, so the caller went on believing
// it held them and a second anchor could take them straight away — exclusivity
// gone, with no error on any path. The distributed locker returns early here, so
// this was also a backend divergence.
func TestLocker_EmptyReacquireKeepsHeldLocks(t *testing.T) {
	l := memory.New()
	ctx := context.Background()

	mustAcquireWithin(t, l, "a", "alice")
	require.NoError(t, l.AcquireLocks(ctx, "a"), "acquiring nothing must succeed")

	timeout, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	require.Error(t, l.AcquireLocks(timeout, "b", "alice"),
		"an empty re-acquisition must not release what the anchor already holds")

	// Releasing "a" must still release alice, i.e. the record was not cleared.
	l.ReleaseLocks(ctx, "a")
	mustAcquireWithin(t, l, "b", "alice")
}

// TestLocker_CancelledCallerIsNotContention pins the classification of a failure
// that has nothing to do with another holder. ErrLockContention was attached to
// every failed acquisition, so cancelling a request — a node shutting down, a
// caller whose deadline elapsed elsewhere — was reported as a lock conflict, and
// counted as one. It was also a fresh divergence in the opposite direction from
// the one this change set fixes: the distributed locker returns a plain context
// error in the same situation.
func TestLocker_CancelledCallerIsNotContention(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		l := memory.New()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := l.AcquireLocks(ctx, "a", "free-id")
		require.Error(t, err, "a caller that has already given up must not be granted locks")
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, errs.ErrLockContention,
			"nothing held free-id, so the failure is the caller's own cancellation")
		require.NotErrorIs(t, err, errs.ErrLockAcquireTimeout)

		// The permit must not have been retained by the failed call.
		mustAcquireWithin(t, l, "b", "free-id")
	})

	t.Run("deadline already elapsed", func(t *testing.T) {
		l := memory.New()
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()

		err := l.AcquireLocks(ctx, "a", "free-id")
		require.Error(t, err)
		require.NotErrorIs(t, err, errs.ErrLockContention)
		require.NotErrorIs(t, err, errs.ErrLockAcquireTimeout,
			"a deadline that elapsed with nothing contending is not a spent waiting budget")

		mustAcquireWithin(t, l, "b", "free-id")
	})
}

// TestLocker_AcquireContendedReportsContention pins the error classification. A
// blocked acquisition that gives up is contention, and callers test for that with
// errors.Is against the shared sentinels; the in-memory locker used to wrap only
// the context error, so every such test was false and the backends disagreed.
func TestLocker_AcquireContendedReportsContention(t *testing.T) {
	l := memory.New()
	require.NoError(t, l.AcquireLocks(context.Background(), "holder", "alice"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := l.AcquireLocks(ctx, "waiter", "alice")

	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrLockContention)
	require.ErrorIs(t, err, errs.ErrLockAcquireTimeout, "a deadline that elapsed while waiting is a timeout")
	assert.Contains(t, err.Error(), "alice", "the error must name the enrollment ID it could not take")
}

// TestLocker_AcquireAllOrNothing verifies the rollback path: a failed
// acquisition must leave none of the IDs it had already taken held, or the
// caller — which got an error and will not call Release — would strand them.
func TestLocker_AcquireAllOrNothing(t *testing.T) {
	l := memory.New()
	ctx := context.Background()
	// "bob" is taken by another anchor, so acquiring {alice, bob} must fail after
	// having already taken "alice".
	require.NoError(t, l.AcquireLocks(ctx, "holder", "bob"))

	timeout, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.Error(t, l.AcquireLocks(timeout, "waiter", "alice", "bob"))

	// "alice" must be free again.
	mustAcquireWithin(t, l, "other", "alice")
}

// TestLocker_AssertLocksHeld documents the interface contract: the in-memory
// locker cannot lose a lock, so the assertion never fails — including for an
// anchor that holds nothing, which callers rely on when they append records
// without having locked anything.
func TestLocker_AssertLocksHeld(t *testing.T) {
	l := memory.New()
	ctx := context.Background()

	require.NoError(t, l.AssertLocksHeld(ctx, "never-acquired"))
	require.NoError(t, l.AcquireLocks(ctx, "a"))
	require.NoError(t, l.AssertLocksHeld(ctx, "a"), "an empty enrollment-ID set is a successful acquisition")
	require.NoError(t, l.AcquireLocks(ctx, "b", "alice"))
	require.NoError(t, l.AssertLocksHeld(ctx, "b"))
	l.ReleaseLocks(ctx, "b")
	require.NoError(t, l.AssertLocksHeld(ctx, "b"))
}

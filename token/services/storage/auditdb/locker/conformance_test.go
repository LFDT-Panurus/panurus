/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/memory"
	lockerpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/postgres"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// The auditor picks its Locker from configuration, so any behavioural difference
// between the backends is a correctness difference between two deployments of the
// same code. Issue #2040 found one that way: a request with no enrollment IDs
// appended fine on the in-memory locker and failed under Postgres. Both backends
// had tests, but no test asserted the same expectation of both — so this file
// exercises the shared contract, and every case must hold for every backend.

// boundedWait is the waiting budget the newBounded lockers are built with. It is
// short enough to assert against in a test, where the production defaults (a
// minute) are not.
const boundedWait = 200 * time.Millisecond

type backend struct {
	name string
	// new returns a fresh locker, or skips the test when its dependencies are
	// unavailable.
	new func(t *testing.T) locker.Locker
	// newBounded returns a fresh locker whose own waiting budget is boundedWait,
	// for the cases that assert on the budget being spent rather than on the
	// caller's context expiring.
	newBounded func(t *testing.T) locker.Locker
}

func backends() []backend {
	return []backend{
		{
			name: "memory",
			new:  func(*testing.T) locker.Locker { return memory.New() },
			newBounded: func(*testing.T) locker.Locker {
				return memory.NewWithConfig(memory.Config{AcquireDeadline: boundedWait})
			},
		},
		{
			name: "postgres",
			new: func(t *testing.T) locker.Locker {
				t.Helper()

				return newPostgresLocker(t, 30*time.Second)
			},
			newBounded: func(t *testing.T) locker.Locker {
				t.Helper()

				return newPostgresLocker(t, boundedWait)
			},
		},
	}
}

// newPostgresLocker starts a throwaway Postgres and returns a locker on its own
// table. Each locker gets a unique table so cases cannot see each other's leases.
func newPostgresLocker(t *testing.T, acquireDeadline time.Duration) locker.Locker {
	t.Helper()
	cfg := postgres.DefaultConfig(postgres.WithDBName("test-locker-conformance"))
	terminate, _, err := postgres.StartPostgres(t.Context(), cfg, nil)
	require.NoError(t, err)
	t.Cleanup(terminate)
	db, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	table := "test_conformance_" + sanitize(t.Name())
	_, _ = db.Exec("DROP TABLE IF EXISTS " + table)
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + table) })

	// The default acquireDeadline passed in by backends().new is deliberately far
	// longer than any context those cases pass in, because that is the production
	// shape: it defaults to a minute, so a request-scoped caller context is nearly
	// always the shorter of the two. A deadline shorter than the caller's would let
	// this backend pass the contention case for the wrong reason — the failure would
	// be attributed to a budget this locker had spent itself, hiding that a
	// caller-driven timeout reported no sentinel at all. The cases that do want to
	// assert on the budget ask for it explicitly, via backends().newBounded.
	l, err := lockerpostgres.New(db, table, lockerpostgres.Config{
		TTL:             30 * time.Second,
		Heartbeat:       10 * time.Second,
		AcquireBackoff:  10 * time.Millisecond,
		AcquireDeadline: acquireDeadline,
		Owner:           "conformance-owner",
	}, stubReplicaID{id: "conformance-owner"})
	require.NoError(t, err)

	return l
}

// sanitize turns a subtest name into a legal, lower-case SQL identifier suffix.
func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		default:
			out = append(out, '_')
		}
	}

	return string(out)
}

// TestConformance_AcquireReleaseRoundTrip is the happy path both backends must
// agree on, including that release makes the enrollment IDs available again.
func TestConformance_AcquireReleaseRoundTrip(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
			l.ReleaseLocks(ctx, "anchor1")

			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice", "bob"),
				"released enrollment IDs must be claimable again")
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_EmptyEnrollmentIDs is the regression test for issue #2040's
// first finding. Acquiring an empty set succeeds on both backends, and — the part
// that used to differ — the pre-write assertion afterwards must succeed too.
// StoreService.Append calls AssertLocksHeld on every write, so under Postgres
// this combination failed with "locks lost before write" for a request whose
// inputs and outputs yielded no enrollment IDs, while the same request appended
// cleanly on the in-memory locker.
func TestConformance_EmptyEnrollmentIDs(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1"), "acquiring nothing must succeed")
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"),
				"an empty enrollment-ID set must not be reported as lost locks")

			l.ReleaseLocks(ctx, "anchor1")
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
		})
	}
}

// TestConformance_AssertLocksHeldWithoutAcquire pins the other half of that
// contract: the assertion reports locks that were lost, not locks that were never
// taken. Auditors that validate and append without calling Audit never acquire
// anything, so this must not be an error on any backend.
func TestConformance_AssertLocksHeldWithoutAcquire(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			require.NoError(t, l.AssertLocksHeld(context.Background(), "never-acquired"))
		})
	}
}

// TestConformance_ReleaseIsIdempotent covers the deferred-release pattern the
// auditor documents: Release runs even on paths that never acquired, and may run
// twice.
func TestConformance_ReleaseIsIdempotent(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()

			l.ReleaseLocks(ctx, "never-acquired")
			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
			l.ReleaseLocks(ctx, "anchor1")
			l.ReleaseLocks(ctx, "anchor1")

			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice"))
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_ReacquireSameAnchorIsIdempotent covers re-acquisition, which
// the Postgres locker treats as a lease refresh. The in-memory locker used to
// deadlock against its own permits here, and to leak the permits of any
// enrollment ID dropped from the anchor's set.
func TestConformance_ReacquireSameAnchorIsIdempotent(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"),
				"re-acquiring the same set under the same anchor must not block")
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))

			l.ReleaseLocks(ctx, "anchor1")
			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice"),
				"release after a re-acquisition must leave nothing held")
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_ContentionIsReported requires a contended acquisition to fail
// in a way callers can classify. auditor.Service inspects these sentinels to
// decide whether retrying can help, so a backend that reports contention as a
// bare context error is not interchangeable with one that does not.
//
// The caller's context here is much shorter than the Postgres backend's
// AcquireDeadline, which is the production shape and the case that used to be
// misclassified: that backend decided what to report from whichever context
// expired first, so a caller-driven timeout returned a bare
// context.DeadlineExceeded with no sentinel while the in-memory locker reported
// contention for the identical situation.
func TestConformance_ContentionIsReported(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()
			require.NoError(t, l.AcquireLocks(ctx, "holder", "alice"))
			t.Cleanup(func() { l.ReleaseLocks(ctx, "holder") })

			waitCtx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
			defer cancel()
			err := l.AcquireLocks(waitCtx, "waiter", "alice")

			require.Error(t, err, "a lock held by another anchor must not be acquirable")
			require.ErrorIs(t, err, errs.ErrLockContention)
			require.ErrorIs(t, err, errs.ErrLockAcquireTimeout,
				"having spent the whole waiting budget must be distinguishable from plain contention")

			// The holder keeps its lock: a failed acquisition takes nothing away.
			require.NoError(t, l.AssertLocksHeld(ctx, "holder"))
		})
	}
}

// TestConformance_EmptyReacquireKeepsHeldLocks pairs with EmptyEnrollmentIDs:
// acquiring nothing succeeds, and must also leave alone whatever the anchor
// already holds. The in-memory locker used to record the empty set instead, which
// released those locks while returning nil — the caller kept believing it held
// them and another anchor could take them immediately, with no error anywhere.
// The Postgres locker returns early, so this was a divergence too.
func TestConformance_EmptyReacquireKeepsHeldLocks(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
			require.NoError(t, l.AcquireLocks(ctx, "anchor1"), "acquiring nothing must succeed")
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"), "the anchor still holds alice")

			waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()
			require.Error(t, l.AcquireLocks(waitCtx, "anchor2", "alice"),
				"an empty re-acquisition must not hand alice to another anchor")

			l.ReleaseLocks(ctx, "anchor1")
			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice"),
				"releasing the anchor must still release alice")
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_CancelledCallerIsNotContention is the mirror image of
// ContentionIsReported: a failure with no other holder involved must not be
// dressed up as a lock conflict. The in-memory locker attached ErrLockContention
// to every failed acquisition, so graceful-shutdown cancellations were reported
// and counted as conflicts, while Postgres returned a plain context error.
func TestConformance_CancelledCallerIsNotContention(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()

			err := l.AcquireLocks(cancelled, "anchor1", "alice")
			require.Error(t, err, "a caller that has given up must not be granted locks")
			require.ErrorIs(t, err, context.Canceled)
			require.NotErrorIs(t, err, errs.ErrLockContention,
				"nothing held alice, so the failure is the caller's own cancellation")

			// The failed call retained nothing, so alice is still free.
			ctx := context.Background()
			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice"))
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_DeduplicatesAndSortsEnrollmentIDs covers the ordering invariant
// both backends rely on for deadlock freedom, plus duplicate handling: a request
// naming the same enrollment ID as input and output must not self-deadlock.
func TestConformance_DeduplicatesAndSortsEnrollmentIDs(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "bob", "alice", "bob", "alice"))
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
			l.ReleaseLocks(ctx, "anchor1")

			// Both IDs must have been released exactly once, in any order.
			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice", "bob"))
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_NarrowingReleasesDroppedEnrollmentIDs covers the shrinking half
// of the refresh contract, and is the gap that hid a Postgres-only defect: its
// acquisition statement only ever inserted, so an ID dropped from a live anchor
// kept its lease row. Both AssertLocksHeld and the heartbeat's renewal count this
// replica's rows for the anchor and require exactly as many as the session
// recorded, so each leftover row failed both — the write below was rejected with
// "locks lost before write", the heartbeat gave up on its first tick, and once the
// TTL passed the abandoned lease became claimable while this replica still
// believed it held it. The in-memory backend released it and had a test saying so;
// this suite only covered the same-set and (then still permitted) widening cases,
// so nothing compared the two backends here.
func TestConformance_NarrowingReleasesDroppedEnrollmentIDs(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))
			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "bob"),
				"narrowing a live anchor's set must succeed")

			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"),
				"the anchor holds exactly what it last asked for, so nothing was lost")

			waitCtx, cancel := context.WithTimeout(ctx, boundedWait)
			defer cancel()
			require.NoError(t, l.AcquireLocks(waitCtx, "anchor2", "alice"),
				"alice was dropped from anchor1, so it must have been released")

			// bob is still held by anchor1 and only becomes free on release.
			require.Error(t, l.AcquireLocks(waitCtx, "anchor3", "bob"))
			l.ReleaseLocks(ctx, "anchor1")
			require.NoError(t, l.AcquireLocks(ctx, "anchor3", "bob"))

			l.ReleaseLocks(ctx, "anchor2")
			l.ReleaseLocks(ctx, "anchor3")
		})
	}
}

// TestConformance_WideningLiveAnchorIsRejected pins the other half: a live
// anchor's set may shrink or stay the same, never grow. Deadlock freedom rests on
// every caller taking shared enrollment IDs in one canonical order, and that order
// can only be imposed over the IDs of a single call — an anchor that keeps earlier
// permits while waiting for new ones holds locks outside it, so two anchors
// widening into each other's IDs wait on each other forever. Both backends must
// refuse it, and must refuse it without disturbing what the anchor already holds.
func TestConformance_WideningLiveAnchorIsRejected(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.new(t)
			ctx := context.Background()

			require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
			err := l.AcquireLocks(ctx, "anchor1", "alice", "bob")
			require.ErrorIs(t, err, errs.ErrLockSetWidened)

			// The refusal took nothing and gave nothing away: anchor1 still holds alice
			// and its locks are not reported as lost, and bob was never reached for.
			require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
			require.NoError(t, l.AcquireLocks(ctx, "anchor2", "bob"))

			waitCtx, cancel := context.WithTimeout(ctx, boundedWait)
			defer cancel()
			require.Error(t, l.AcquireLocks(waitCtx, "anchor3", "alice"),
				"anchor1 must still hold alice")

			l.ReleaseLocks(ctx, "anchor1")
			l.ReleaseLocks(ctx, "anchor2")
		})
	}
}

// TestConformance_BackendBoundsItsOwnWait covers the requirement that an
// implementation bound its own waiting rather than relying on the caller's
// context. The in-memory locker had no budget: a caller with no deadline blocked
// forever, and no failure it produced could carry ErrLockAcquireTimeout — the
// signal auditor.Service reads to tell "already waited in full" from "worth
// another attempt". Every case above passes a timeout context, which is exactly
// what hid that, so this one deliberately does not.
func TestConformance_BackendBoundsItsOwnWait(t *testing.T) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			l := b.newBounded(t)
			ctx := context.Background()
			require.NoError(t, l.AcquireLocks(ctx, "holder", "alice"))
			t.Cleanup(func() { l.ReleaseLocks(ctx, "holder") })

			done := make(chan error, 1)
			go func() { done <- l.AcquireLocks(ctx, "waiter", "alice") }()

			select {
			case err := <-done:
				require.ErrorIs(t, err, errs.ErrLockContention)
				require.ErrorIs(t, err, errs.ErrLockAcquireTimeout,
					"a backend that spent its whole budget must say so")
			case <-time.After(30 * time.Second):
				t.Fatal("AcquireLocks did not bound its own wait for a caller with no deadline")
			}

			require.NoError(t, l.AssertLocksHeld(ctx, "holder"), "the holder keeps its lock")
		})
	}
}

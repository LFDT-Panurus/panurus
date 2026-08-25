/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/errs"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/id"
	lockerpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/postgres"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReplicaID struct{ id string }

func (s stubReplicaID) ID() string { return s.id }

// unconnectedDB returns a valid, non-nil *sql.DB that is never dialled: the
// DSN parses but points nowhere. Owner validation happens before any query, so
// construction-time failures can be asserted without a live database.
func unconnectedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://user:pass@127.0.0.1:1/none")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func startPostgres(t *testing.T) *sql.DB {
	t.Helper()
	cfg := postgres.DefaultConfig(postgres.WithDBName("test-locker"))
	terminate, _, err := postgres.StartPostgres(t.Context(), cfg, nil)
	require.NoError(t, err)
	t.Cleanup(terminate)
	db, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func cleanTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	_, _ = db.Exec("DROP TABLE IF EXISTS " + table)
}

func newLocker(t *testing.T, db *sql.DB, table string, cfg lockerpostgres.Config) *lockerpostgres.Locker {
	t.Helper()
	l, err := lockerpostgres.New(db, table, cfg, stubReplicaID{id: cfg.Owner})
	require.NoError(t, err)

	return l
}

func TestLocker_AcquireRelease(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_ar"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 5 * time.Second, Heartbeat: 2 * time.Second, Owner: "owner-1",
	})

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))
	require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
	l.ReleaseLocks(ctx, "anchor1")

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE anchor = $1", "anchor1").Scan(&count))
	assert.Equal(t, 0, count)
}

func TestLocker_Contention(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_ct"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l1 := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, AcquireBackoff: 50 * time.Millisecond,
		AcquireDeadline: 500 * time.Millisecond, Heartbeat: 10 * time.Second,
		Owner: "owner-1",
	})
	l2 := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, AcquireBackoff: 50 * time.Millisecond,
		AcquireDeadline: 500 * time.Millisecond, Heartbeat: 10 * time.Second,
		Owner: "owner-2",
	})

	ctx := context.Background()
	require.NoError(t, l1.AcquireLocks(ctx, "a1", "alice"))

	err := l2.AcquireLocks(ctx, "a2", "alice")
	require.ErrorIs(t, err, errs.ErrLockAcquireTimeout)

	l1.ReleaseLocks(ctx, "a1")

	require.NoError(t, l2.AcquireLocks(ctx, "a3", "alice"))
	l2.ReleaseLocks(ctx, "a3")
}

func TestLocker_ConcurrentNonOverlapping(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_cno"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 5 * time.Second, Heartbeat: 2 * time.Second, Owner: "owner-1",
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		assert.NoError(t, l.AcquireLocks(ctx, "a1", "alice"))
		time.Sleep(10 * time.Millisecond)
		l.ReleaseLocks(ctx, "a1")
	}()
	go func() {
		defer wg.Done()
		assert.NoError(t, l.AcquireLocks(ctx, "a2", "bob"))
		time.Sleep(10 * time.Millisecond)
		l.ReleaseLocks(ctx, "a2")
	}()
	wg.Wait()
}

func TestLocker_OwnerScopingAcrossReplicas(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_os"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	cfg := lockerpostgres.Config{
		TTL: 30 * time.Second, AcquireBackoff: 50 * time.Millisecond,
		AcquireDeadline: 200 * time.Millisecond, Heartbeat: 10 * time.Second,
	}
	cfg.Owner = "owner-1"
	l1 := newLocker(t, db, table, cfg)
	cfg.Owner = "owner-2"
	l2 := newLocker(t, db, table, cfg)

	ctx := context.Background()
	require.NoError(t, l1.AcquireLocks(ctx, "anchor1", "alice"))

	// owner-2 must not be able to release owner-1's leases, even for the same anchor
	l2.ReleaseLocks(ctx, "anchor1")

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE anchor = $1 AND owner = $2", "anchor1", "owner-1").Scan(&count))
	assert.Equal(t, 1, count, "owner-2 released a lease it does not own")
	require.NoError(t, l1.AssertLocksHeld(ctx, "anchor1"))

	l1.ReleaseLocks(ctx, "anchor1")
}

// TestLocker_SameOwnerDifferentAnchorsCannotShareEID is the regression test for
// issue #2033: two audits on the same node (hence the same owner) for different
// anchors that share an enrollment ID. The second acquisition used to overwrite
// the first one's live lease and report success, leaving both callers believing
// they held it exclusively. It must now be plain contention.
func TestLocker_SameOwnerDifferentAnchorsCannotShareEID(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_same_owner"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, AcquireBackoff: 25 * time.Millisecond,
		AcquireDeadline: 300 * time.Millisecond, Heartbeat: 10 * time.Second,
		Owner: "owner-1",
	})

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))

	// anchor2 shares "alice" with anchor1 and belongs to the same owner.
	err := l.AcquireLocks(ctx, "anchor2", "alice")
	require.ErrorIs(t, err, errs.ErrLockContention, "a live lease of another anchor must not be stealable")
	require.ErrorIs(t, err, errs.ErrLockAcquireTimeout)

	// anchor1 still owns the shared lease, unchanged.
	var anchor, owner string
	require.NoError(t, db.QueryRow("SELECT anchor, owner FROM "+table+" WHERE eid = $1", "alice").Scan(&anchor, &owner))
	assert.Equal(t, "anchor1", anchor, "the shared enrollment ID must still be held by the first anchor")
	assert.Equal(t, "owner-1", owner)
	require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))

	// The failed attempt left nothing behind. This is asserted on the table rather
	// than through AssertLocksHeld, which reports lost locks and not absent ones:
	// an anchor holding nothing has nothing to lose, so it succeeds by contract.
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE anchor = $1", "anchor2").Scan(&count))
	assert.Equal(t, 0, count)

	l.ReleaseLocks(ctx, "anchor1")

	// Once released, the same enrollment ID is claimable by the other anchor.
	require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice"))
	l.ReleaseLocks(ctx, "anchor2")
}

// TestLocker_SameOwnerSameAnchorRefreshesLease verifies the case the conflict
// clause is meant to allow: re-acquiring the same enrollment IDs under the same
// anchor is idempotent and pushes the lease deadline out.
func TestLocker_SameOwnerSameAnchorRefreshesLease(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_reacquire"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, AcquireBackoff: 25 * time.Millisecond,
		AcquireDeadline: 300 * time.Millisecond, Heartbeat: time.Hour,
		Owner: "owner-1",
	})

	ctx := context.Background()
	expiry := func() time.Time {
		t.Helper()
		var at time.Time
		require.NoError(t, db.QueryRow("SELECT expires_at FROM "+table+" WHERE eid = $1", "alice").Scan(&at))

		return at
	}

	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
	first := expiry()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"), "re-acquiring one's own anchor must succeed")
	assert.True(t, expiry().After(first), "re-acquisition must refresh the lease deadline")

	require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
	l.ReleaseLocks(ctx, "anchor1")
}

// TestLocker_ExpiredLeaseIsClaimableByAnotherAnchor verifies the crash-recovery
// path still works: once a lease expires it may be taken over, even by a
// different anchor of the same owner. Heartbeat is longer than the TTL so no
// renewal interferes.
func TestLocker_ExpiredLeaseIsClaimableByAnotherAnchor(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_expired"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 200 * time.Millisecond, AcquireBackoff: 25 * time.Millisecond,
		AcquireDeadline: 5 * time.Second, Heartbeat: time.Hour,
		Owner: "owner-1",
	})

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
	time.Sleep(400 * time.Millisecond) // outlive the lease

	require.NoError(t, l.AcquireLocks(ctx, "anchor2", "alice"))

	var anchor string
	require.NoError(t, db.QueryRow("SELECT anchor FROM "+table+" WHERE eid = $1", "alice").Scan(&anchor))
	assert.Equal(t, "anchor2", anchor)
	l.ReleaseLocks(ctx, "anchor2")
}

// TestLocker_ConcurrentSharedEIDSingleWinner is the concurrency shape the issue
// describes: several audits in flight on one node, all touching the same
// enrollment ID. Exactly one may hold it; the rest must time out contended.
func TestLocker_ConcurrentSharedEIDSingleWinner(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_concurrent"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, AcquireBackoff: 25 * time.Millisecond,
		AcquireDeadline: 300 * time.Millisecond, Heartbeat: time.Hour,
		Owner: "owner-1",
	})

	const audits = 4
	ctx := context.Background()
	anchors := []string{"a0", "a1", "a2", "a3"}
	results := make([]error, audits)

	var wg sync.WaitGroup
	wg.Add(audits)
	for i := range audits {
		go func() {
			defer wg.Done()
			// No release: whoever wins keeps the lease for the whole test.
			results[i] = l.AcquireLocks(ctx, anchors[i], "alice")
		}()
	}
	wg.Wait()

	winners := make([]string, 0, audits)
	for i, err := range results {
		if err == nil {
			winners = append(winners, anchors[i])

			continue
		}
		require.ErrorIs(t, err, errs.ErrLockContention, "a losing audit must report contention")
	}
	require.Len(t, winners, 1, "exactly one audit may hold the shared enrollment ID, got %v", winners)

	var anchor string
	require.NoError(t, db.QueryRow("SELECT anchor FROM "+table+" WHERE eid = $1", "alice").Scan(&anchor))
	assert.Equal(t, winners[0], anchor, "the table must reflect the one audit that reported success")
	require.NoError(t, l.AssertLocksHeld(ctx, winners[0]))

	l.ReleaseLocks(ctx, winners[0])
}

// TestLocker_NoLocksHeldAssertsSuccessfully is the regression test for the
// backend divergence in issue #2040. AssertLocksHeld reports locks that were
// lost, not locks that were never taken, so an anchor holding nothing must
// succeed. It used to return ErrLockNotHeld for any anchor without a session,
// which failed StoreService.Append with "locks lost before write" for two
// legitimate flows — a request whose inputs and outputs yield no enrollment IDs,
// and an auditor that validates and appends without calling Audit — while both
// succeeded under the in-memory locker.
func TestLocker_NoLocksHeldAssertsSuccessfully(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_nolocks"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 5 * time.Second, Heartbeat: 2 * time.Second, Owner: "owner-1",
	})
	ctx := context.Background()

	require.NoError(t, l.AssertLocksHeld(ctx, "never-acquired"),
		"an anchor that never locked anything has nothing to lose")

	require.NoError(t, l.AcquireLocks(ctx, "empty-anchor"), "acquiring no enrollment IDs must succeed")
	require.NoError(t, l.AssertLocksHeld(ctx, "empty-anchor"),
		"an empty enrollment-ID set must not look like a lost lease")

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	assert.Equal(t, 0, count, "an empty acquisition must not write lease rows")

	l.ReleaseLocks(ctx, "empty-anchor")
	require.NoError(t, l.AssertLocksHeld(ctx, "empty-anchor"))

	// A real acquisition still tracks its leases, and losing one is still reported.
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
	require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
	_, err := db.Exec("DELETE FROM "+table+" WHERE eid = $1", "alice")
	require.NoError(t, err)
	require.ErrorIs(t, l.AssertLocksHeld(ctx, "anchor1"), errs.ErrLockNotHeld,
		"a lease that vanished must still be reported as lost")
}

// TestLocker_ContentionBacksOffWithoutBusyPolling checks the shape of the retry
// loop, not just its outcome. A contended acquisition used to poll at a fixed
// AcquireBackoff for the whole deadline — hundreds of round trips per attempt, on
// an interval identical across replicas so contenders stayed in lockstep. With
// exponential backoff the same wait costs a fraction of the attempts, while the
// deadline is still honoured.
func TestLocker_ContentionBacksOffWithoutBusyPolling(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_backoff"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, Heartbeat: time.Hour, Owner: "owner-1",
		AcquireBackoff:  10 * time.Millisecond,
		AcquireDeadline: 700 * time.Millisecond,
	})

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "holder", "alice"))
	t.Cleanup(func() { l.ReleaseLocks(ctx, "holder") })

	start := time.Now()
	err := l.AcquireLocks(ctx, "waiter", "alice")
	elapsed := time.Since(start)

	require.ErrorIs(t, err, errs.ErrLockAcquireTimeout)
	require.ErrorIs(t, err, errs.ErrLockContention)
	assert.GreaterOrEqual(t, elapsed, 700*time.Millisecond, "the acquire deadline must be spent before giving up")
	assert.Less(t, elapsed, 3*time.Second,
		"the deadline bounds the whole wait, including a backoff sleep that would overrun it")

	// The failed attempt must leave the holder's lease untouched.
	var anchor string
	require.NoError(t, db.QueryRow("SELECT anchor FROM "+table+" WHERE eid = $1", "alice").Scan(&anchor))
	assert.Equal(t, "holder", anchor)
}

// TestLocker_ContentionReportedWhenCallerDeadlineIsShorter pins the sentinel on
// the shape production actually has. AcquireDeadline defaults to a minute, so a
// request-scoped caller context is nearly always the shorter of the two, and the
// classification used to be decided by whichever expired first: when the caller's
// did, the contention sentinels were skipped entirely and a bare
// context.DeadlineExceeded came back. The effect was that this backend almost
// never reported contention in production — while the in-memory locker always did
// — so auditor.Service could not tell a lock conflict from a dead caller.
func TestLocker_ContentionReportedWhenCallerDeadlineIsShorter(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_shortcaller"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, Heartbeat: time.Hour, Owner: "owner-1",
		AcquireBackoff: 10 * time.Millisecond,
		// Far longer than the caller's context below, as in a default deployment.
		AcquireDeadline: 30 * time.Second,
	})

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "holder", "alice"))
	t.Cleanup(func() { l.ReleaseLocks(ctx, "holder") })

	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	err := l.AcquireLocks(waitCtx, "waiter", "alice")

	require.Error(t, err)
	require.ErrorIs(t, err, errs.ErrLockContention,
		"alice was held by another anchor, which is contention however the wait ended")
	require.ErrorIs(t, err, errs.ErrLockAcquireTimeout,
		"the caller's budget is spent, so repeating the call adds delay rather than a fresh chance")
}

// TestLocker_UncontendedTimeoutIsNotContention is the other side of that
// classification. A deadline that elapses while nothing holds the IDs is not a
// lock conflict, and reporting one hid the real cause: the outcome was derived
// from the error merely containing context.DeadlineExceeded, so a query the
// deadline killed on an overloaded database came back as ErrLockContention with
// the original error discarded — an infrastructure failure permanently labelled
// as contention, with no diagnostics, and one auditor.Service refuses to retry.
func TestLocker_UncontendedTimeoutIsNotContention(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_uncontended"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, Heartbeat: time.Hour, Owner: "owner-1",
		AcquireBackoff: time.Millisecond,
		// Too short to complete a round trip, so the attempt fails on the deadline
		// while "alice" is free and nothing is contending for it.
		AcquireDeadline: time.Nanosecond,
	})

	err := l.AcquireLocks(context.Background(), "anchor1", "alice")
	require.Error(t, err)
	require.NotErrorIs(t, err, errs.ErrLockContention,
		"no other anchor held alice, so this is a timeout and not a lock conflict")

	// Nothing was left behind, and the ID is still claimable.
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	assert.Equal(t, 0, count)
}

// TestLocker_RejectedReacquireKeepsLiveSession covers what a re-acquisition that
// cannot be granted must leave behind. The failure path used to release the anchor
// unconditionally, so a re-acquisition that lost a race deleted the lease rows of
// the *live* session it was re-acquiring for — and left that session's record and
// heartbeat in place. The next renewal then matched none of its rows, logged the
// leases as lost and exited, and the caller's next legitimate Append failed its
// pre-write assertion with "locks lost before write" even though nothing had been
// stolen.
//
// Widening a live anchor is now refused outright (see the Locker contract), which
// is how the case below is turned away, so the release-on-failure path is reached
// only by a database error. The outcome under test is the same either way, and the
// one that matters: a re-acquisition this locker did not grant must leave the
// session's leases exactly as they were.
func TestLocker_RejectedReacquireKeepsLiveSession(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_reacquire_fail"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	cfg := lockerpostgres.Config{
		TTL: 30 * time.Second, Heartbeat: time.Hour,
		AcquireBackoff:  10 * time.Millisecond,
		AcquireDeadline: 300 * time.Millisecond,
	}
	cfg.Owner = "owner-1"
	mine := newLocker(t, db, table, cfg)
	cfg.Owner = "owner-2"
	theirs := newLocker(t, db, table, cfg)

	ctx := context.Background()
	require.NoError(t, mine.AcquireLocks(ctx, "anchor1", "alice"))
	t.Cleanup(func() { mine.ReleaseLocks(ctx, "anchor1") })

	// Another replica holds "bob", so widening anchor1 to {alice, bob} could not have
	// succeeded on its merits either.
	require.NoError(t, theirs.AcquireLocks(ctx, "other-anchor", "bob"))
	t.Cleanup(func() { theirs.ReleaseLocks(ctx, "other-anchor") })

	err := mine.AcquireLocks(ctx, "anchor1", "alice", "bob")
	require.Error(t, err, "anchor1 already holds alice, so it cannot also take bob")
	require.ErrorIs(t, err, errs.ErrLockSetWidened)

	// The live session is intact: its lease row survived and the assertion passes.
	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM "+table+" WHERE anchor = $1 AND owner = $2", "anchor1", "owner-1").Scan(&count))
	assert.Equal(t, 1, count, "the failed re-acquisition must not delete the live session's lease")
	require.NoError(t, mine.AssertLocksHeld(ctx, "anchor1"),
		"a failed re-acquisition must not make the session's existing locks look lost")
}

// TestLocker_ReleaseWithCancelledContextStillDeletes covers the common shape of a
// release: it is deferred, so by the time it runs the caller's context is often
// already done. Passing that context straight to the DELETE made the statement
// fail silently, leaving the enrollment IDs locked against every replica until
// their TTL expired — a 30-second stall by default for a lock that was released
// on time.
func TestLocker_ReleaseWithCancelledContextStillDeletes(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_release_cancelled"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	l := newLocker(t, db, table, lockerpostgres.Config{
		TTL: 30 * time.Second, Heartbeat: time.Hour, Owner: "owner-1",
		AcquireBackoff:  10 * time.Millisecond,
		AcquireDeadline: 300 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
	cancel()

	l.ReleaseLocks(ctx, "anchor1")

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	assert.Equal(t, 0, count, "a release on an already-cancelled context must still delete the leases")
	require.NoError(t, l.AcquireLocks(context.Background(), "anchor2", "alice"),
		"alice must be claimable again immediately, not only once the lease TTL expires")
	l.ReleaseLocks(context.Background(), "anchor2")
}

func TestLocker_NilDB(t *testing.T) {
	_, err := lockerpostgres.New(nil, "t", lockerpostgres.Config{}, stubReplicaID{id: "owner"})
	require.Error(t, err)
}

func TestLocker_OwnerRequired(t *testing.T) {
	tests := []struct {
		name      string
		cfgOwner  string
		replicaID id.ReplicaIDProvider
	}{
		{name: "empty config owner and empty replica id", cfgOwner: "", replicaID: stubReplicaID{id: ""}},
		{name: "nil replica id provider", cfgOwner: "", replicaID: nil},
		{name: "blank config owner and blank replica id", cfgOwner: "   ", replicaID: stubReplicaID{id: "  "}},
		{name: "blank replica id", cfgOwner: "", replicaID: stubReplicaID{id: " \t"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			l, err := lockerpostgres.New(
				unconnectedDB(t),
				"test_eid_lease_owner",
				lockerpostgres.Config{Owner: test.cfgOwner},
				test.replicaID,
			)
			require.ErrorIs(t, err, errs.ErrLockerOwnerRequired)
			assert.Nil(t, l)
		})
	}
}

func TestLocker_OwnerFromReplicaID(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_orid"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	// no cfg.Owner: the replica id is used as the lease owner
	l, err := lockerpostgres.New(db, table, lockerpostgres.Config{
		TTL: 5 * time.Second, Heartbeat: 2 * time.Second,
	}, stubReplicaID{id: "replica-7"})
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice"))
	t.Cleanup(func() { l.ReleaseLocks(ctx, "anchor1") })

	var owner string
	require.NoError(t, db.QueryRow("SELECT owner FROM "+table+" WHERE eid = $1", "alice").Scan(&owner))
	assert.Equal(t, "replica-7", owner)
}

// TestLocker_NarrowingDeletesDroppedLeaseRows pins the row-level outcome the
// conformance suite checks through the interface. The acquisition statement only
// ever inserted, so an enrollment ID dropped from a live anchor kept its lease row.
// Both AssertLocksHeld and renewLeases count this replica's un-expired rows for the
// anchor and require exactly as many as the session recorded, so a single leftover
// row failed both: writes were rejected with "locks lost before write", the
// heartbeat gave up on its first tick, and once the TTL elapsed the abandoned lease
// expired and became claimable by another replica while this one still believed it
// held it.
func TestLocker_NarrowingDeletesDroppedLeaseRows(t *testing.T) {
	db := startPostgres(t)
	table := "test_eid_lease_narrowing"
	cleanTable(t, db, table)
	t.Cleanup(func() { cleanTable(t, db, table) })

	cfg := lockerpostgres.Config{
		TTL: 30 * time.Second, Heartbeat: time.Hour,
		AcquireBackoff:  10 * time.Millisecond,
		AcquireDeadline: 300 * time.Millisecond,
		Owner:           "owner-1",
	}
	l := newLocker(t, db, table, cfg)

	ctx := context.Background()
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))
	t.Cleanup(func() { l.ReleaseLocks(ctx, "anchor1") })
	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "bob"))

	var eids []string
	rows, err := db.Query("SELECT eid FROM "+table+" WHERE anchor = $1 AND owner = $2", "anchor1", "owner-1")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var eid string
		require.NoError(t, rows.Scan(&eid))
		eids = append(eids, eid)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"bob"}, eids, "the lease dropped from the anchor must be deleted, not left behind")

	// The counts the session, its heartbeat and its assertions all rely on now agree.
	require.NoError(t, l.AssertLocksHeld(ctx, "anchor1"))
}

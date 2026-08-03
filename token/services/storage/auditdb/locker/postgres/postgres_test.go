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

	// The failed attempt left nothing behind.
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE anchor = $1", "anchor2").Scan(&count))
	assert.Equal(t, 0, count)
	require.ErrorIs(t, l.AssertLocksHeld(ctx, "anchor2"), errs.ErrLockNotHeld)

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

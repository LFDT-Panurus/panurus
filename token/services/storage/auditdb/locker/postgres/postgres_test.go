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
	lockerpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/postgres"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReplicaID struct{ id string }

func (s stubReplicaID) ID() string { return s.id }

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

func TestLocker_NilDB(t *testing.T) {
	_, err := lockerpostgres.New(nil, "t", lockerpostgres.Config{}, stubReplicaID{id: "owner"})
	require.Error(t, err)
}

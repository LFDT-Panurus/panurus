/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestTokenLockStore_AcquireCleanupLeadership_ConcurrentReplicas verifies
// that when two "replicas" (independent TokenLockStore instances for the
// same TMS, sharing the underlying connection pool but each acquiring its
// own session via AcquireCleanupLeadership) race for the same cleanup lock,
// exactly one wins. Uses the real public constructor throughout, so the
// cleanup lock id is derived internally exactly as it is in production
// (from the table prefix), not injected by the test. See #1798.
func TestTokenLockStore_AcquireCleanupLeadership_ConcurrentReplicas(t *testing.T) {
	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("test-cleanup-leadership"))
	terminate, _, err := fscpostgres.StartPostgres(t.Context(), cfg, nil)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	t.Cleanup(terminate)

	db, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	tables, err := sqlcommon.GetTableNames("")
	require.NoError(t, err)
	rwdb := &common2.RWDB{ReadDB: db, WriteDB: db}

	// TokenLockStore's schema has a foreign key to the Tokens table, so the
	// base token store schema must exist first.
	tokenStore, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)
	require.NoError(t, err)
	require.NoError(t, tokenStore.CreateSchema())

	// Two independent TokenLockStore instances for the same TMS (same table
	// prefix), simulating two replicas. They share the connection pool, but
	// AcquireCleanupLeadership takes a dedicated connection (session)
	// internally, so this exercises real pg_try_advisory_lock contention,
	// not just two calls on one session.
	replicaA, err := NewTokenLockStore(rwdb, tables)
	require.NoError(t, err)
	replicaB, err := NewTokenLockStore(rwdb, tables)
	require.NoError(t, err)
	require.NoError(t, replicaA.CreateSchema())

	require.Equal(t, replicaA.cleanupLockID, replicaB.cleanupLockID,
		"replicas for the same TMS must derive the same cleanup lock id")

	var wg sync.WaitGroup
	results := make([]bool, 2)
	leaderships := make([]interface{ Close() error }, 2)
	errs := make([]error, 2)

	acquire := func(i int, store *TokenLockStore) {
		defer wg.Done()
		l, acquired, err := store.AcquireCleanupLeadership(context.Background())
		results[i] = acquired
		errs[i] = err
		if acquired {
			leaderships[i] = l
		}
	}

	wg.Add(2)
	go acquire(0, replicaA)
	go acquire(1, replicaB)
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.NotEqual(t, results[0], results[1], "exactly one replica should acquire leadership, got: A=%v B=%v", results[0], results[1])

	for _, l := range leaderships {
		if l != nil {
			require.NoError(t, l.Close())
		}
	}

	l3, acquired3, err := replicaA.AcquireCleanupLeadership(context.Background())
	require.NoError(t, err)
	require.True(t, acquired3, "leadership should be acquirable again after release")
	require.NoError(t, l3.Close())
}

// TestTokenLockStore_AcquireCleanupLeadership_DistinctTMS verifies that two
// TokenLockStores for *different* TMSes (different table prefixes) derive
// different cleanup lock ids, and so do not contend with each other at all
// - this is the exact bug flagged in review: a single global lock id would
// have caused every TMS on a node to compete for the same lock, so only one
// TMS across the whole fleet would ever win cleanup on any tick. See #1798.
func TestTokenLockStore_AcquireCleanupLeadership_DistinctTMS(t *testing.T) {
	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("test-cleanup-leadership-distinct-tms"))
	terminate, _, err := fscpostgres.StartPostgres(t.Context(), cfg, nil)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	t.Cleanup(terminate)

	db, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rwdb := &common2.RWDB{ReadDB: db, WriteDB: db}

	// Same prefix (one shared persistence block), different TMS identity
	// params - this is the actual scenario the manager creates in
	// production (db/manager.go), and the one that was broken: Prefix
	// alone is identical for both, only the formatted table name (which
	// bakes in the params) differs. See review discussion on #1982.
	tablesA, err := sqlcommon.GetTableNames("tsdk", "netA", "chA", "nsA")
	require.NoError(t, err)
	tablesB, err := sqlcommon.GetTableNames("tsdk", "netB", "chB", "nsB")
	require.NoError(t, err)

	tokenStoreA, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tablesA, NewConditionInterpreter(), nil)
	require.NoError(t, err)
	require.NoError(t, tokenStoreA.CreateSchema())
	tokenStoreB, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tablesB, NewConditionInterpreter(), nil)
	require.NoError(t, err)
	require.NoError(t, tokenStoreB.CreateSchema())

	tmsA, err := NewTokenLockStore(rwdb, tablesA)
	require.NoError(t, err)
	require.NoError(t, tmsA.CreateSchema())
	tmsB, err := NewTokenLockStore(rwdb, tablesB)
	require.NoError(t, err)
	require.NoError(t, tmsB.CreateSchema())

	require.NotEqual(t, tmsA.cleanupLockID, tmsB.cleanupLockID,
		"distinct TMSes must derive distinct cleanup lock ids, or one TMS's cleanup would starve the other's")
	require.NotEqual(t, tmsA.lockID, tmsB.lockID,
		"distinct TMSes must derive distinct schema-creation lock ids too - a regression back to a "+
			"prefix-only or global constant would pass the cleanupLockID check above but not this one")
	require.NotEqual(t, tmsA.lockID, tmsA.cleanupLockID,
		"schema-creation and cleanup locks on the same store must be distinct")
	require.NotEqual(t, tmsB.lockID, tmsB.cleanupLockID,
		"schema-creation and cleanup locks on the same store must be distinct")

	// both should acquire leadership independently and concurrently, since
	// they are coordinating over different locks
	lA, acquiredA, err := tmsA.AcquireCleanupLeadership(context.Background())
	require.NoError(t, err)
	require.True(t, acquiredA)
	defer func() { _ = lA.Close() }()

	lB, acquiredB, err := tmsB.AcquireCleanupLeadership(context.Background())
	require.NoError(t, err)
	require.True(t, acquiredB, "TMS B should acquire leadership independently of TMS A")
	defer func() { _ = lB.Close() }()
}

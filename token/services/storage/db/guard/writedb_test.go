/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard_test

import (
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/guard"
	"github.com/stretchr/testify/require"
)

type fakeSQLAuditTxStore struct {
	fakeAuditTxStore
	db *sql.DB
}

func (f *fakeSQLAuditTxStore) WriteDB() *sql.DB { return f.db }

func TestWrapAuditTransactionKeepsWriteDB(t *testing.T) {
	db := &sql.DB{}
	wrapped := guard.WrapAuditTransaction(&fakeSQLAuditTxStore{db: db}, guard.DefaultPolicy())

	p, ok := wrapped.(locker.WriteDBProvider)
	require.True(t, ok, "guarded SQL audit store must still expose WriteDB")
	require.Same(t, db, p.WriteDB())
}

func TestWrapAuditTransactionWithoutWriteDB(t *testing.T) {
	wrapped := guard.WrapAuditTransaction(&fakeAuditTxStore{}, guard.DefaultPolicy())

	_, ok := wrapped.(locker.WriteDBProvider)
	require.False(t, ok, "a non-SQL audit store must not gain WriteDB")
}

// fakeSkipSQLAuditTxStore is a SQL audit store that also reports skipCreateTable.
type fakeSkipSQLAuditTxStore struct {
	fakeSQLAuditTxStore
	skip bool
}

func (f *fakeSkipSQLAuditTxStore) PrefixedTableName(name string) string { return "fsc_" + name }
func (f *fakeSkipSQLAuditTxStore) SkipCreateTable() bool                { return f.skip }

type stubReplicaID struct{}

func (stubReplicaID) ID() string { return "r1" }

func TestWrapAuditTransactionKeepsSkipCreateTable(t *testing.T) {
	for _, skip := range []bool{true, false} {
		wrapped := guard.WrapAuditTransaction(&fakeSkipSQLAuditTxStore{skip: skip}, guard.DefaultPolicy())

		p, ok := wrapped.(locker.SkipCreateTableProvider)
		require.True(t, ok, "guarded SQL audit store must expose SkipCreateTable")
		require.Equal(t, skip, p.SkipCreateTable())
	}
}

func TestWrapAuditTransactionSkipCreateTableDefault(t *testing.T) {
	wrapped := guard.WrapAuditTransaction(&fakeSQLAuditTxStore{}, guard.DefaultPolicy())

	p, ok := wrapped.(locker.SkipCreateTableProvider)
	require.True(t, ok)
	require.False(t, p.SkipCreateTable(), "a store without the flag keeps the locker default")
}

// TestGuardedStoreLockerSkipCreateTable builds the locker from a guarded store,
// as auditdb does in production.
func TestGuardedStoreLockerSkipCreateTable(t *testing.T) {
	cfg := locker.Config{Backend: locker.BackendPostgres}

	t.Run("skip", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		store := &fakeSkipSQLAuditTxStore{fakeSQLAuditTxStore: fakeSQLAuditTxStore{db: db}, skip: true}
		l, err := locker.NewFromConfig(cfg, guard.WrapAuditTransaction(store, guard.DefaultPolicy()), stubReplicaID{})
		require.NoError(t, err)
		require.NotNil(t, l)
		// no DDL may run against the store's database
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no skip", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS fsc_eid_leases")).
			WillReturnResult(sqlmock.NewResult(0, 0))

		store := &fakeSkipSQLAuditTxStore{fakeSQLAuditTxStore: fakeSQLAuditTxStore{db: db}}
		l, err := locker.NewFromConfig(cfg, guard.WrapAuditTransaction(store, guard.DefaultPolicy()), stubReplicaID{})
		require.NoError(t, err)
		require.NotNil(t, l)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

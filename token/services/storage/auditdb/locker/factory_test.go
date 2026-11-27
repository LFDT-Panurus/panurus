/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locker_test

import (
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReplicaID struct{ id string }

func (s stubReplicaID) ID() string { return s.id }

// writeDBOnly satisfies locker.WriteDBProvider but not driver.AuditTransactionStore.
type writeDBOnly struct{}

func (writeDBOnly) WriteDB() *sql.DB { return nil }

func TestNewFromConfig_MemoryBackends(t *testing.T) {
	// the memory backend has no owner concept, so an empty replica id must
	// keep working — the owner requirement is postgres-only
	for _, backend := range []locker.Backend{locker.BackendMemory, ""} {
		t.Run(string(backend), func(t *testing.T) {
			l, err := locker.NewFromConfig(locker.Config{Backend: backend}, nil, stubReplicaID{id: ""})
			require.NoError(t, err)
			assert.NotNil(t, l)
		})
	}
}

func TestNewFromConfig_DefaultConfigIsMemory(t *testing.T) {
	cfg := locker.DefaultConfig()
	assert.Equal(t, locker.BackendMemory, cfg.Backend)

	l, err := locker.NewFromConfig(cfg, nil, stubReplicaID{id: ""})
	require.NoError(t, err)
	assert.NotNil(t, l)
}

func TestNewFromConfig_UnknownBackend(t *testing.T) {
	l, err := locker.NewFromConfig(locker.Config{Backend: "nosuchbackend"}, nil, stubReplicaID{id: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown locker backend")
	assert.Nil(t, l)
}

func TestNewFromConfig_PostgresRequiresSQLStore(t *testing.T) {
	cfg := locker.Config{Backend: locker.BackendPostgres}

	// a store exposing no *sql.DB
	l, err := locker.NewFromConfig(cfg, struct{}{}, stubReplicaID{id: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WriteDBProvider")
	assert.Nil(t, l)

	// a store exposing a *sql.DB but not an AuditTransactionStore
	l, err = locker.NewFromConfig(cfg, writeDBOnly{}, stubReplicaID{id: "r1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AuditTransactionStore")
	assert.Nil(t, l)
}

// sqlAuditStore is a minimal SQL-backed audit store. Only WriteDB,
// PrefixedTableName and SkipCreateTable are implemented.
type sqlAuditStore struct {
	dbdriver.AuditTransactionStore
	db   *sql.DB
	skip bool
}

func (s sqlAuditStore) WriteDB() *sql.DB                     { return s.db }
func (s sqlAuditStore) PrefixedTableName(name string) string { return "fsc_" + name }
func (s sqlAuditStore) SkipCreateTable() bool                { return s.skip }

func TestNewFromConfig_PostgresHonoursStoreSkipCreateTable(t *testing.T) {
	cfg := locker.Config{Backend: locker.BackendPostgres}

	t.Run("skip", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		l, err := locker.NewFromConfig(cfg, sqlAuditStore{db: db, skip: true}, stubReplicaID{id: "r1"})
		require.NoError(t, err)
		assert.NotNil(t, l)
		// no DDL may run against the store's database
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no skip", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS fsc_eid_leases")).
			WillReturnResult(sqlmock.NewResult(0, 0))

		l, err := locker.NewFromConfig(cfg, sqlAuditStore{db: db}, stubReplicaID{id: "r1"})
		require.NoError(t, err)
		assert.NotNil(t, l)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

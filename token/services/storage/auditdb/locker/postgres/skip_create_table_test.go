/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres_test

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	lockerpostgres "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/postgres"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_SkipCreateTable checks that New runs the lease-table DDL only when
// SkipCreateTable is unset.
func TestNew_SkipCreateTable(t *testing.T) {
	t.Run("skip runs no SQL", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		l, err := lockerpostgres.New(db, "eid_leases", lockerpostgres.Config{SkipCreateTable: true}, stubReplicaID{id: "r1"})
		require.NoError(t, err)
		assert.NotNil(t, l)
		// sqlmock fails any unexpected statement and ExpectationsWereMet reports
		// none were run.
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no skip creates the table", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS eid_leases")).
			WillReturnResult(sqlmock.NewResult(0, 0))

		l, err := lockerpostgres.New(db, "eid_leases", lockerpostgres.Config{}, stubReplicaID{id: "r1"})
		require.NoError(t, err)
		assert.NotNil(t, l)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no skip reports DDL failure", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS eid_leases")).
			WillReturnError(errors.New("permission denied for schema public"))

		_, err = lockerpostgres.New(db, "eid_leases", lockerpostgres.Config{}, stubReplicaID{id: "r1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create auditor eid lease table")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("owner check runs before skip", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		_, err = lockerpostgres.New(db, "eid_leases", lockerpostgres.Config{SkipCreateTable: true}, stubReplicaID{id: ""})
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

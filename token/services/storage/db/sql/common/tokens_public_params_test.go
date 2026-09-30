/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

const (
	publicParamsSelect = "SELECT raw FROM PP WHERE raw_hash = \\$1"
	publicParamsInsert = "INSERT INTO PP \\(raw, raw_hash, stored_at\\) " +
		"VALUES \\(\\$1, \\$2, \\$3\\) ON CONFLICT DO NOTHING"
)

func newTestPublicParamsStore(t *testing.T) *sqlFixture[*TokenStore] {
	t.Helper()

	return newSQLFixture(t, func(db *sql.DB, _ sqlmock.Sqlmock) *TokenStore {
		return &TokenStore{
			readDB:  db,
			writeDB: db,
			table:   tokenTables{PublicParams: "PP"},
			ci:      newTestInterpreter(),
		}
	})
}

// TestStorePublicParams_InsertIsIdempotent asserts the insert carries
// ON CONFLICT DO NOTHING. The pre-read cannot make the write safe on its own:
// two callers can both observe "not present" and then race the insert, and
// without the conflict clause one of them would surface a raw primary-key
// violation instead of the benign success this package reports elsewhere.
func TestStorePublicParams_InsertIsIdempotent(t *testing.T) {
	f := newTestPublicParamsStore(t)

	f.mock.ExpectQuery(publicParamsSelect).
		WillReturnRows(sqlmock.NewRows([]string{"raw"}))
	f.mock.ExpectExec(publicParamsInsert).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, f.store.StorePublicParams(t.Context(), []byte("pp")))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestStorePublicParams_ConflictIsSuccess covers the concurrent-insert case the
// conflict clause exists for: the row appeared between the read and the write,
// the insert becomes a no-op, and that must not be reported as a failure.
func TestStorePublicParams_ConflictIsSuccess(t *testing.T) {
	f := newTestPublicParamsStore(t)

	f.mock.ExpectQuery(publicParamsSelect).
		WillReturnRows(sqlmock.NewRows([]string{"raw"}))
	f.mock.ExpectExec(publicParamsInsert).
		WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, f.store.StorePublicParams(t.Context(), []byte("pp")))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestStorePublicParams_AlreadyStoredSkipsInsert covers the common repeat call:
// the parameters are already there, so no write is issued at all.
func TestStorePublicParams_AlreadyStoredSkipsInsert(t *testing.T) {
	f := newTestPublicParamsStore(t)

	f.mock.ExpectQuery(publicParamsSelect).
		WillReturnRows(sqlmock.NewRows([]string{"raw"}).AddRow([]byte("pp")))

	require.NoError(t, f.store.StorePublicParams(t.Context(), []byte("pp")))
	// No ExpectExec: an insert here would fail the call.
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestStorePublicParams_InsertFailurePropagates(t *testing.T) {
	f := newTestPublicParamsStore(t)

	f.mock.ExpectQuery(publicParamsSelect).
		WillReturnRows(sqlmock.NewRows([]string{"raw"}))
	f.mock.ExpectExec(publicParamsInsert).WillReturnError(sql.ErrConnDone)

	err := f.store.StorePublicParams(t.Context(), []byte("pp"))

	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

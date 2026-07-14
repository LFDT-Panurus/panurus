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

func TestPreparedStmtHolder_PrepareAndReuse(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mockDB.ExpectPrepare("SELECT 1").
		ExpectQuery().
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	mockDB.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))

	h := newPreparedStmtHolder[string]()
	buildCalls := 0
	buildQuery := func() (string, []any, error) {
		buildCalls++

		return "SELECT 1", nil, nil
	}

	rows, err := h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count())

	// second call, same key: statement reused (only one PrepareContext
	// expected above), buildQuery still invoked for its args
	rows, err = h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count())
	require.Equal(t, 2, buildCalls)

	require.NoError(t, mockDB.ExpectationsWereMet())
}

func TestPreparedStmtHolder_DistinctKeys(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mockDB.ExpectPrepare("SELECT 1").
		ExpectQuery().
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	mockDB.ExpectPrepare("SELECT 2").
		ExpectQuery().
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(2))

	h := newPreparedStmtHolder[string]()

	rows, err := h.Execute(t.Context(), db, "a", func() (string, []any, error) { return "SELECT 1", nil, nil })
	require.NoError(t, err)
	require.NoError(t, rows.Close())

	rows, err = h.Execute(t.Context(), db, "b", func() (string, []any, error) { return "SELECT 2", nil, nil })
	require.NoError(t, err)
	require.NoError(t, rows.Close())

	require.Equal(t, 2, h.Count())
	require.NoError(t, mockDB.ExpectationsWereMet())
}

func TestPreparedStmtHolder_FallsBackOnPrepareError(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mockDB.ExpectPrepare("SELECT 1").WillReturnError(sql.ErrConnDone)
	mockDB.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))

	h := newPreparedStmtHolder[string]()
	rows, err := h.Execute(t.Context(), db, "k", func() (string, []any, error) { return "SELECT 1", nil, nil })
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	// prepare failed, so nothing got cached
	require.Equal(t, 0, h.Count())
	require.NoError(t, mockDB.ExpectationsWereMet())
}

func TestPreparedStmtHolder_BuildQueryErrorPropagates(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	h := newPreparedStmtHolder[string]()
	buildErr := sql.ErrNoRows
	_, err = h.Execute(t.Context(), db, "k", func() (string, []any, error) { return "", nil, buildErr })
	require.ErrorIs(t, err, buildErr)
}

func TestPreparedStmtHolder_Close(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mockDB.ExpectPrepare("SELECT 1").
		ExpectQuery().
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	mockDB.ExpectClose()

	h := newPreparedStmtHolder[string]()
	rows, err := h.Execute(t.Context(), db, "k", func() (string, []any, error) { return "SELECT 1", nil, nil })
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count())

	require.NoError(t, h.Close())
	require.Equal(t, 0, h.Count())
}

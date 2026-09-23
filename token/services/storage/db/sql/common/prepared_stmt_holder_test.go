/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count())

	// second call, same key: statement reused (only one PrepareContext
	// expected above), buildQuery still invoked for its args
	rows, err = h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Err())
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
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	rows, err = h.Execute(t.Context(), db, "b", func() (string, []any, error) { return "SELECT 2", nil, nil })
	require.NoError(t, err)
	require.NoError(t, rows.Err())
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
	require.NoError(t, rows.Err())
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
	//nolint:rowserrcheck // Execute fails while building the query, so it never returns rows
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
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count())

	require.NoError(t, h.Close())
	require.Equal(t, 0, h.Count())
}

// TestPreparedStmtHolder_EvictsAfterQueryError asserts the cache self-heals: a
// statement the server no longer recognises is dropped, so the next call
// re-prepares it instead of taking the unprepared fallback from then on.
func TestPreparedStmtHolder_EvictsAfterQueryError(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// First call: prepare succeeds, the query on it fails because the statement
	// no longer exists server-side, the unprepared fallback succeeds.
	mockDB.ExpectPrepare("SELECT 1").
		ExpectQuery().
		WillReturnError(errors.New(`ERROR: prepared statement "stmt1" does not exist (SQLSTATE 26000)`))
	mockDB.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	// Second call: the holder must prepare again rather than reuse the broken
	// statement or go straight to the fallback.
	mockDB.ExpectPrepare("SELECT 1").
		ExpectQuery().
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))

	h := newPreparedStmtHolder[string]()
	buildQuery := func() (string, []any, error) { return "SELECT 1", nil, nil }

	rows, err := h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, 0, h.Count(), "the failed statement must not stay cached")

	rows, err = h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count(), "the re-prepared statement must be cached again")

	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestPreparedStmtHolder_KeepsStatementAfterOrdinaryQueryError is the converse
// of TestPreparedStmtHolder_EvictsAfterQueryError: an execute failure that says
// nothing about the statement's validity must leave it cached. Evicting here
// would re-prepare a perfectly good statement on the next call, and where the
// failure is permanent but unrelated to validity it would add a DEALLOCATE to
// every single call - strictly worse than not evicting at all.
func TestPreparedStmtHolder_KeepsStatementAfterOrdinaryQueryError(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// First call: prepare succeeds, the query fails for an ordinary reason, the
	// unprepared fallback succeeds.
	prepared := mockDB.ExpectPrepare("SELECT 1")
	prepared.ExpectQuery().WillReturnError(sql.ErrConnDone)
	mockDB.ExpectQuery("SELECT 1").
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	// Second call: another query on the *same* prepared statement, and no second
	// ExpectPrepare. The holder must reuse the cached statement; had it evicted,
	// it would prepare again and sqlmock would fail the test.
	prepared.ExpectQuery().WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))

	h := newPreparedStmtHolder[string]()
	buildQuery := func() (string, []any, error) { return "SELECT 1", nil, nil }

	rows, err := h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count(), "a statement that is still valid must stay cached")

	rows, err = h.Execute(t.Context(), db, "k", buildQuery)
	require.NoError(t, err)
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, 1, h.Count(), "the cached statement must be reused, not re-prepared")

	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestIsInvalidPreparedStmt pins which errors are treated as invalidating the
// cached statement. Only these cause an eviction.
func TestIsInvalidPreparedStmt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "nil", err: nil, expected: false},
		{
			name:     "postgres invalid_sql_statement_name by message",
			err:      errors.New(`ERROR: prepared statement "s1" does not exist (SQLSTATE 26000)`),
			expected: true,
		},
		{
			name:     "postgres duplicate_prepared_statement by message",
			err:      errors.New(`ERROR: prepared statement "s1" already exists (SQLSTATE 42P05)`),
			expected: true,
		},
		{name: "connection done is not a statement problem", err: sql.ErrConnDone, expected: false},
		{name: "no rows is not a statement problem", err: sql.ErrNoRows, expected: false},
		{
			name:     "constraint violation is not a statement problem",
			err:      errors.New("ERROR: insert violates foreign key constraint (SQLSTATE 23503)"),
			expected: false,
		},
		{
			name:     "unrelated message mentioning existence",
			err:      errors.New("relation \"tokens\" does not exist"),
			expected: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.expected, isInvalidPreparedStmt(tc.err))
		})
	}
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func newTestTokenTransaction(t *testing.T) *sqlFixture[*TokenTransaction] {
	t.Helper()

	return newSQLFixture(t, func(db *sql.DB, mock sqlmock.Sqlmock) *TokenTransaction {
		mock.ExpectBegin()
		tx, err := db.Begin()
		require.NoError(t, err)

		return &TokenTransaction{
			ci:    newTestInterpreter(),
			table: &tokenTables{Tokens: "TOKENS"},
			tx:    tx,
		}
	})
}

// TestSetSpendableBySupportedTokenFormats_OnlyTouchesDisagreeingRows pins the
// shape of both updates: each carries a predicate on the spendable flag, so rows
// already in the target state are not rewritten. This used to clear the flag on
// every row of the table unconditionally before setting the supported ones.
func TestSetSpendableBySupportedTokenFormats_OnlyTouchesDisagreeingRows(t *testing.T) {
	f := newTestTokenTransaction(t)

	// Clear: spendable rows whose format is not supported (or is NULL).
	f.mock.ExpectExec(
		"UPDATE TOKENS SET spendable = \\$1 WHERE \\(spendable = \\$2\\) AND "+
			"\\(\\(NOT \\(\\(ledger_type\\) IN \\(\\(\\$3\\), \\(\\$4\\)\\)\\)\\) OR \\(ledger_type IS NULL\\)\\)").
		WithArgs(false, true, token.Format("CLEAR"), token.Format("CLEAR1")).
		WillReturnResult(sqlmock.NewResult(0, 3))

	// Set: non-spendable rows whose format is supported.
	f.mock.ExpectExec(
		"UPDATE TOKENS SET spendable = \\$1 WHERE \\(spendable = \\$2\\) AND "+
			"\\(\\(ledger_type\\) IN \\(\\(\\$3\\), \\(\\$4\\)\\)\\)").
		WithArgs(true, false, token.Format("CLEAR"), token.Format("CLEAR1")).
		WillReturnResult(sqlmock.NewResult(0, 2))

	require.NoError(t, f.store.SetSpendableBySupportedTokenFormats(t.Context(), []token.Format{"CLEAR", "CLEAR1"}))
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// TestSetSpendableBySupportedTokenFormats_NoFormatsClearsAll covers the empty
// list: nothing is spendable. It needs its own branch because cond.In degrades
// to a tautology on an empty value set, which would otherwise make "not
// supported" match no row at all - and, before the flag predicates were added,
// made the second update mark every token spendable.
func TestSetSpendableBySupportedTokenFormats_NoFormatsClearsAll(t *testing.T) {
	f := newTestTokenTransaction(t)

	f.mock.ExpectExec("UPDATE TOKENS SET spendable = \\$1 WHERE spendable = \\$2").
		WithArgs(false, true).
		WillReturnResult(sqlmock.NewResult(0, 5))

	require.NoError(t, f.store.SetSpendableBySupportedTokenFormats(t.Context(), nil))
	// Exactly one statement: there is no format left to mark spendable.
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestSetSpendableBySupportedTokenFormats_ClearFailurePropagates(t *testing.T) {
	f := newTestTokenTransaction(t)

	f.mock.ExpectExec("UPDATE TOKENS").WillReturnError(sql.ErrConnDone)

	err := f.store.SetSpendableBySupportedTokenFormats(t.Context(), []token.Format{"CLEAR"})

	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

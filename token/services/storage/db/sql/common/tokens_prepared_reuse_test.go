/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// stubCondInterpreter is a Postgres-equivalent CondInterpreter for tests
// that construct a TokenStore without a real DB driver package (avoiding an
// import cycle, since postgres/sqlite both import common). Mirrors
// postgres.interpreter exactly: SpendableTokensIteratorBy's HasTokenDetails
// does exercise InTuple (for LedgerTokenFormats membership), so this must
// produce real, correctly bound SQL rather than a placeholder/no-op.
type stubCondInterpreter struct{}

func (stubCondInterpreter) TimeOffset(duration time.Duration, sb common3.Builder) {
	sb.WriteString("NOW()")
	if duration == 0 {
		return
	}
	sign := '+'
	if duration < 0 {
		sign = '-'
	}
	sb.WriteRune(' ').
		WriteRune(sign).
		WriteString(" INTERVAL '").
		WriteString(strconv.Itoa(int(math.Abs(duration.Seconds())))).
		WriteString(" seconds'")
}

func (stubCondInterpreter) InTuple(fields []common3.Serializable, vals []common3.Tuple, sb common3.Builder) {
	if len(vals) == 0 || len(fields) == 0 {
		return
	}
	ors := make([]cond.Condition, len(vals))
	for j, tuple := range vals {
		ands := make([]cond.Condition, len(tuple))
		for k, val := range tuple {
			ands[k] = cond.CmpVal(fields[k], "=", val)
		}
		ors[j] = cond.And(ands...)
	}
	sb.WriteConditionSerializable(cond.Or(ors...), stubCondInterpreter{})
}

// TestUnspentTokensIteratorByPreparedReuse verifies, without a real DB, that
// repeated calls with the same argument shape reuse one prepared statement,
// and different shapes prepare distinct statements (see #1183).
func TestUnspentTokensIteratorByPreparedReuse(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := &TokenStore{
		readDB: db,
		table: tokenTables{
			Tokens:    "tokens",
			Ownership: "ownership",
		},
		ci:                 stubCondInterpreter{},
		unspentTokensStmts: newPreparedStmtHolder[string](),
	}

	cols := []string{"tx_id", "idx", "owner_raw", "token_type", "quantity", "wallet_id"}
	queryPattern := "(?s)SELECT.*tokens.*ownership.*UNION ALL.*"

	// same shape (walletID+tokenType present) called 3 times -> prepared once
	mockDB.ExpectPrepare(queryPattern).
		ExpectQuery().WillReturnRows(sqlmock.NewRows(cols))
	mockDB.ExpectQuery(queryPattern).WillReturnRows(sqlmock.NewRows(cols))
	mockDB.ExpectQuery(queryPattern).WillReturnRows(sqlmock.NewRows(cols))

	for range 3 {
		it, err := store.UnspentTokensIteratorBy(t.Context(), "wallet0", tokentype.Type("GOLD"))
		require.NoError(t, err)
		it.Close()
	}
	require.Equal(t, 1, store.PreparedStmtCount())

	// different shape (walletID present, tokenType absent) -> a second statement
	mockDB.ExpectPrepare(queryPattern).
		ExpectQuery().WillReturnRows(sqlmock.NewRows(cols))

	it, err := store.UnspentTokensIteratorBy(t.Context(), "wallet0", "")
	require.NoError(t, err)
	it.Close()
	require.Equal(t, 2, store.PreparedStmtCount())

	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestSpendableTokensIteratorByPreparedReuse verifies, without a real DB,
// that repeated calls with the same argument shape reuse one prepared
// statement, and different shapes prepare distinct statements (see #1919).
func TestSpendableTokensIteratorByPreparedReuse(t *testing.T) {
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := &TokenStore{
		readDB: db,
		table: tokenTables{
			Tokens: "tokens",
		},
		ci:                   stubCondInterpreter{},
		spendableTokensStmts: newPreparedStmtHolder[string](),
	}

	cols := []string{"tx_id", "idx", "token_type", "quantity", "owner_wallet_id"}
	queryPattern := "(?s)SELECT.*tokens.*"

	// same shape (walletID+tokenType present) called 3 times -> prepared once
	mockDB.ExpectPrepare(queryPattern).
		ExpectQuery().WillReturnRows(sqlmock.NewRows(cols))
	mockDB.ExpectQuery(queryPattern).WillReturnRows(sqlmock.NewRows(cols))
	mockDB.ExpectQuery(queryPattern).WillReturnRows(sqlmock.NewRows(cols))

	for range 3 {
		it, err := store.SpendableTokensIteratorBy(t.Context(), "wallet0", tokentype.Type("GOLD"))
		require.NoError(t, err)
		it.Close()
	}
	require.Equal(t, 1, store.spendableTokensStmts.Count())

	// different shape (walletID+tokenType both absent) -> a second statement
	mockDB.ExpectPrepare(queryPattern).
		ExpectQuery().WillReturnRows(sqlmock.NewRows(cols))

	it, err := store.SpendableTokensIteratorBy(t.Context(), "", "")
	require.NoError(t, err)
	it.Close()
	require.Equal(t, 2, store.spendableTokensStmts.Count())

	require.NoError(t, mockDB.ExpectationsWereMet())
}

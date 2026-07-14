/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	common3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/common"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// stubCondInterpreter is a minimal CondInterpreter for tests that never
// exercise TimeOffset or InTuple - UnspentTokensIteratorBy's query uses
// neither (see the call-site analysis on #1183).
type stubCondInterpreter struct{}

func (stubCondInterpreter) TimeOffset(time.Duration, common3.Builder) {
	panic("not used by UnspentTokensIteratorBy")
}

func (stubCondInterpreter) InTuple([]common3.Serializable, []common3.Tuple, common3.Builder) {
	panic("not used by UnspentTokensIteratorBy")
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

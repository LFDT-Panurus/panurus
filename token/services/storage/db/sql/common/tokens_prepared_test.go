/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"math/big"
	"testing"

	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func TestUnspentTokensStmtKey(t *testing.T) {
	require.Equal(t, "11", unspentTokensStmtKey("wallet0", tokentype.Type("GOLD")))
	require.Equal(t, "10", unspentTokensStmtKey("wallet0", ""))
	require.Equal(t, "00", unspentTokensStmtKey("", ""))
	require.Equal(t, "01", unspentTokensStmtKey("", tokentype.Type("GOLD")))

	// same shape, different values -> same key (statement is shared)
	require.Equal(t,
		unspentTokensStmtKey("walletA", tokentype.Type("GOLD")),
		unspentTokensStmtKey("walletB", tokentype.Type("SILVER")),
	)
}

func TestTokenStore_PreparedStmtCount_NoDB(t *testing.T) {
	store := &TokenStore{unspentTokensStmts: newPreparedStmtHolder[string]()}
	require.Equal(t, 0, store.PreparedStmtCount())
}

// TestBalanceStmtKey checks that the balance cache key separates every pair of shapes whose
// SQL differs, and only those. buildBalanceQuery renders the amount bounds, so a key that
// ignored them would hand a bounded call the unbounded statement, silently summing the whole
// wallet.
func TestBalanceStmtKey(t *testing.T) {
	db := &TokenStore{
		table: tokenTables{Tokens: "tokens", Ownership: "token_ownership"},
		ci:    newTestInterpreter(),
	}

	one, two := big.NewInt(1), big.NewInt(2)
	shapes := []driver2.QueryTokenDetailsParams{
		{},
		{WalletID: "alice"},
		{TokenType: tokentype.Type("TST")},
		{WalletID: "alice", TokenType: tokentype.Type("TST")},
		{MinAmount: one},
		{MaxAmount: one},
		{MinAmount: one, MaxAmount: one},
		{WalletID: "alice", TokenType: tokentype.Type("TST"), MinAmount: one, MaxAmount: two},
	}

	keys := make(map[string]string, len(shapes))
	for _, shape := range shapes {
		query, _ := buildBalanceQuery(db, shape)
		key := balanceStmtKey(shape)
		if previous, seen := keys[key]; seen {
			require.Equal(t, previous, query, "shapes sharing key %q must share SQL", key)

			continue
		}
		keys[key] = query
	}
	require.Len(t, keys, len(shapes))

	// Conversely, two calls of the same shape differ only in their bound values, so they
	// share a key and therefore a prepared statement.
	shape := driver2.QueryTokenDetailsParams{
		WalletID: "alice", TokenType: tokentype.Type("TST"), MinAmount: one, MaxAmount: two,
	}
	other := shape
	other.WalletID, other.TokenType, other.MinAmount, other.MaxAmount = "bob", tokentype.Type("ABC"), two, one
	require.Equal(t, balanceStmtKey(shape), balanceStmtKey(other))
}

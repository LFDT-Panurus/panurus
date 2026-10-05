/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"math/big"
	"testing"

	driver2 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSQLOnlyTokenStore returns a store wired with just enough state to render SQL. It has no
// database behind it, so only the query builders may be exercised against it.
func newSQLOnlyTokenStore() *TokenStore {
	return &TokenStore{
		table: tokenTables{Tokens: "tokens"},
		ci:    newTestInterpreter(),
	}
}

// TestBuildSpendableTokensQuery pins the SQL each SpendableTokensQuery shape renders. The
// point of the bounds, the ORDER BY and the LIMIT is that the database applies them, so a
// silently dropped clause would turn a bounded window back into the full-set scan #2020 is
// about without changing any result the dbtest suite checks.
func TestBuildSpendableTokensQuery(t *testing.T) {
	db := newSQLOnlyTokenStore()

	const baseWhere = "WHERE (owner = $1) AND (token_type = $2) AND (owner_wallet_id = $3) AND (is_deleted = $4) AND (spendable = $5)"
	const selectFrom = "SELECT tx_id, idx, token_type, quantity, owner_wallet_id FROM tokens "

	for _, tc := range []struct {
		name         string
		params       driver2.SpendableTokensQuery
		expectedSQL  string
		expectedArgs []any
	}{
		{
			// The zero value must render exactly what SpendableTokensIteratorBy rendered
			// before: no amount predicate, no ORDER BY, no LIMIT.
			name:         "zero value",
			params:       driver2.SpendableTokensQuery{},
			expectedSQL:  selectFrom + "WHERE (owner = $1) AND (is_deleted = $2) AND (spendable = $3)",
			expectedArgs: []any{true, false, true},
		},
		{
			name:         "wallet and type",
			params:       driver2.SpendableTokensQuery{WalletID: "alice", TokenType: token.Type("TST")},
			expectedSQL:  selectFrom + baseWhere,
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true},
		},
		{
			name: "amount range",
			params: driver2.SpendableTokensQuery{
				WalletID:  "alice",
				TokenType: token.Type("TST"),
				MinAmount: big.NewInt(5),
				MaxAmount: big.NewInt(100),
			},
			expectedSQL:  selectFrom + baseWhere + " AND (amount >= $6) AND (amount <= $7)",
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true, "5", "100"},
		},
		{
			name: "ascending",
			params: driver2.SpendableTokensQuery{
				WalletID: "alice", TokenType: token.Type("TST"), Order: driver2.AmountAscending,
			},
			expectedSQL:  selectFrom + baseWhere + " ORDER BY amount ASC",
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true},
		},
		{
			name: "descending with limit",
			params: driver2.SpendableTokensQuery{
				WalletID: "alice", TokenType: token.Type("TST"), Order: driver2.AmountDescending, Limit: 3,
			},
			expectedSQL:  selectFrom + baseWhere + " ORDER BY amount DESC LIMIT $6",
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true, 3},
		},
		{
			// Zero means "no cap", so no LIMIT clause is emitted at all.
			name: "zero limit is no limit",
			params: driver2.SpendableTokensQuery{
				WalletID: "alice", TokenType: token.Type("TST"), Limit: 0,
			},
			expectedSQL:  selectFrom + baseWhere,
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true},
		},
		{
			// A negative limit must not reach the builder: it reads common3.ZeroLimit (-1) as
			// an explicit LIMIT 0, which would return nothing instead of everything.
			name: "negative limit is no limit",
			params: driver2.SpendableTokensQuery{
				WalletID: "alice", TokenType: token.Type("TST"), Limit: -1,
			},
			expectedSQL:  selectFrom + baseWhere,
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true},
		},
		{
			name: "everything",
			params: driver2.SpendableTokensQuery{
				WalletID:  "alice",
				TokenType: token.Type("TST"),
				MinAmount: big.NewInt(1),
				MaxAmount: big.NewInt(2),
				Order:     driver2.AmountDescending,
				Limit:     7,
			},
			expectedSQL:  selectFrom + baseWhere + " AND (amount >= $6) AND (amount <= $7) ORDER BY amount DESC LIMIT $8",
			expectedArgs: []any{true, token.Type("TST"), "alice", false, true, "1", "2", 7},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, args := buildSpendableTokensQuery(db, tc.params)
			assert.Equal(t, tc.expectedSQL, query)
			assert.Equal(t, tc.expectedArgs, args)
		})
	}
}

// TestBuildSpendableTokensIteratorByQueryUnchanged checks that routing
// SpendableTokensIteratorBy through the new builder left its SQL byte-identical, so the
// selector's hot path is untouched by #2020.
func TestBuildSpendableTokensIteratorByQueryUnchanged(t *testing.T) {
	db := newSQLOnlyTokenStore()

	for _, walletID := range []string{"", "alice"} {
		for _, typ := range []token.Type{"", "TST"} {
			legacyQuery, legacyArgs := buildSpendableTokensIteratorByQuery(db, walletID, typ)
			query, args := buildSpendableTokensQuery(db, driver2.SpendableTokensQuery{
				WalletID: walletID, TokenType: typ,
			})
			assert.Equal(t, query, legacyQuery)
			assert.Equal(t, args, legacyArgs)
			assert.NotContains(t, legacyQuery, "ORDER BY")
			assert.NotContains(t, legacyQuery, "LIMIT")
			assert.NotContains(t, legacyQuery, "amount")
		}
	}
}

// TestSpendableTokensStmtKey checks that the prepared-statement cache key separates every
// pair of shapes whose SQL differs, and only those. A key that collided across shapes would
// hand a cached statement SQL that does not match its arguments.
func TestSpendableTokensStmtKey(t *testing.T) {
	db := newSQLOnlyTokenStore()

	one, two := big.NewInt(1), big.NewInt(2)
	shapes := []driver2.SpendableTokensQuery{
		{},
		{WalletID: "alice"},
		{TokenType: token.Type("TST")},
		{WalletID: "alice", TokenType: token.Type("TST")},
		{MinAmount: one},
		{MaxAmount: one},
		{MinAmount: one, MaxAmount: one},
		{Order: driver2.AmountAscending},
		{Order: driver2.AmountDescending},
		{Limit: 1},
		{WalletID: "alice", TokenType: token.Type("TST"), MinAmount: one, Order: driver2.AmountDescending, Limit: 1},
	}

	// Distinct SQL implies distinct keys.
	keys := make(map[string]string, len(shapes))
	for _, shape := range shapes {
		query, _ := buildSpendableTokensQuery(db, shape)
		key := spendableTokensStmtKey(shape)
		if previous, seen := keys[key]; seen {
			require.Equal(t, previous, query, "shapes sharing key %q must share SQL", key)

			continue
		}
		keys[key] = query
	}
	assert.Len(t, keys, len(shapes))

	// Conversely, only the values differ between two calls of the same shape, so they share a
	// key and therefore a prepared statement.
	shape := driver2.SpendableTokensQuery{
		WalletID: "alice", TokenType: token.Type("TST"), MinAmount: one, Order: driver2.AmountDescending, Limit: 1,
	}
	other := shape
	other.WalletID, other.TokenType, other.MinAmount, other.Limit = "bob", token.Type("ABC"), two, 9
	assert.Equal(t, spendableTokensStmtKey(shape), spendableTokensStmtKey(other))

	// An unknown order keys as unordered, matching the SQL the builder emits for it.
	unknown := driver2.SpendableTokensQuery{Order: driver2.AmountOrder(99)}
	assert.Equal(t, spendableTokensStmtKey(driver2.SpendableTokensQuery{}), spendableTokensStmtKey(unknown))
	unknownSQL, _ := buildSpendableTokensQuery(db, unknown)
	unorderedSQL, _ := buildSpendableTokensQuery(db, driver2.SpendableTokensQuery{})
	assert.Equal(t, unorderedSQL, unknownSQL)
}

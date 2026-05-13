/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// newLimitTestStore builds a TokenStore over a sqlmock connection, using the
// Postgres-equivalent stubCondInterpreter from tokens_prepared_reuse_test.go.
func newLimitTestStore(t *testing.T) (*TokenStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)

	store := &TokenStore{
		readDB: db,
		table: tokenTables{
			Tokens:    "tokens",
			Ownership: "ownership",
		},
		ci:                 stubCondInterpreter{},
		unspentTokensStmts: newPreparedStmtHolder[string](),
	}

	return store, mockDB, func() { _ = db.Close() }
}

// TestUnspentTokensIteratorByLimitUsesNumberedPlaceholders pins the dialect of
// the limited query. The builder emits $1, $2, ... for every parameter; a
// hand-appended literal "?" for the LIMIT mixes dialects and is a syntax error
// on PostgreSQL ("syntax error at end of input", SQLSTATE 42601), which would
// break every token selection on a Postgres-backed node.
func TestUnspentTokensIteratorByLimitUsesNumberedPlaceholders(t *testing.T) {
	store, _, cleanup := newLimitTestStore(t)
	defer cleanup()

	query, args := buildUnspentTokensIteratorByQuery(store, "wallet0", tokentype.Type("GOLD"), 25)

	require.NotContains(t, query, "?", "query must not contain a '?' placeholder: %s", query)
	require.Contains(t, query, "ORDER BY amount DESC LIMIT $"+strconv.Itoa(len(args)))
	require.Equal(t, 25, args[len(args)-1], "limit must be bound as the last parameter")

	// Every parameter must have a corresponding $N placeholder, and no
	// placeholder may reference an index beyond the parameter list.
	for i := 1; i <= len(args); i++ {
		require.Contains(t, query, "$"+strconv.Itoa(i), "missing placeholder $%d in %s", i, query)
	}
	require.NotContains(t, query, "$"+strconv.Itoa(len(args)+1))
}

// TestUnspentTokensIteratorByLimitDedupsInSQL verifies the limited query uses
// UNION (deduplicating) rather than UNION ALL. A directly-owned token matches
// both branches, so with UNION ALL the LIMIT would count pre-dedup rows and
// dedupedTokenRowsIterator would then collapse them, surfacing only about half
// the requested rows — which the selector misreads as "no more tokens".
func TestUnspentTokensIteratorByLimitDedupsInSQL(t *testing.T) {
	store, _, cleanup := newLimitTestStore(t)
	defer cleanup()

	limited, _ := buildUnspentTokensIteratorByQuery(store, "wallet0", tokentype.Type("GOLD"), 25)
	require.NotContains(t, limited, "UNION ALL")
	require.Contains(t, limited, " UNION ")

	// The unlimited path keeps UNION ALL (dedup happens in the iterator) and
	// must carry no ORDER BY / LIMIT clause.
	unlimited, args := buildUnspentTokensIteratorByQuery(store, "wallet0", tokentype.Type("GOLD"), 0)
	require.Contains(t, unlimited, " UNION ALL ")
	require.NotContains(t, unlimited, "LIMIT")
	require.NotContains(t, unlimited, "ORDER BY")
	require.NotContains(t, unlimited, "?")
	for i := 1; i <= len(args); i++ {
		require.Contains(t, unlimited, "$"+strconv.Itoa(i))
	}
}

// TestUnspentTokensIteratorByLimitBypassesStmtCache verifies the limited path
// runs an ad-hoc query and never poisons the prepared-statement cache, which
// is keyed only by argument shape and would otherwise hand a limit-specific
// statement to a later unlimited call of the same shape.
func TestUnspentTokensIteratorByLimitBypassesStmtCache(t *testing.T) {
	store, mockDB, cleanup := newLimitTestStore(t)
	defer cleanup()

	cols := []string{"tx_id", "idx", "owner_raw", "token_type", "quantity", "amount", "wallet_id"}
	mockDB.ExpectQuery(`(?s)SELECT.*UNION.*ORDER BY amount DESC LIMIT \$\d+`).
		WillReturnRows(sqlmock.NewRows(cols))

	it, err := store.UnspentTokensIteratorBy(t.Context(), "wallet0", tokentype.Type("GOLD"), 25)
	require.NoError(t, err)
	it.Close()

	require.Equal(t, 0, store.PreparedStmtCount(), "limited query must not be cached as a prepared statement")
	require.NoError(t, mockDB.ExpectationsWereMet())
}

// TestUnspentTokensIteratorByLimitPlaceholderCountMatchesArgs runs the limited
// query through every argument shape, checking the placeholder/arg accounting
// stays consistent when optional predicates drop out of the WHERE clause.
func TestUnspentTokensIteratorByLimitPlaceholderCountMatchesArgs(t *testing.T) {
	store, _, cleanup := newLimitTestStore(t)
	defer cleanup()

	for _, tc := range []struct {
		name      string
		walletID  string
		tokenType tokentype.Type
	}{
		{"wallet and type", "wallet0", "GOLD"},
		{"wallet only", "wallet0", ""},
		{"type only", "", "GOLD"},
		{"neither", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, args := buildUnspentTokensIteratorByQuery(store, tc.walletID, tc.tokenType, 7)
			require.NotContains(t, query, "?")
			require.Equal(t, 7, args[len(args)-1])
			require.Equal(t, len(args), strings.Count(query, "$"),
				"one placeholder per arg expected in %s", query)
		})
	}
}

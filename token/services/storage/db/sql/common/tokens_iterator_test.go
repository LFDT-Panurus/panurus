/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	"github.com/stretchr/testify/require"
)

// unspentTokenTestColumns mirrors the projection of both UnspentTokensIteratorBy
// branches, in the order dedupedTokenRowsIterator.Next scans them. amount sits
// between quantity and wallet_id: it is selected only so the UNION result can be
// ordered numerically, and is scanned into a discarded destination.
var unspentTokenTestColumns = []string{"tx_id", "idx", "owner", "token_type", "quantity", "amount", "wallet_id"}

// The fixture below hard-codes the column list, so a projection change that does
// not reach it would make every row fail with "expected N destination arguments
// in Scan" — a shape mismatch that says nothing about the behaviour under test.
// Fail loudly and specifically instead.
func TestUnspentTokenFixtureMatchesProjection(t *testing.T) {
	tokenTable, ownershipTable := q.Table("tokens"), q.Table("ownership")
	require.Len(t, unspentTokenTestColumns, len(unspentTokenFields(tokenTable, ownershipTable)),
		"unspentTokenTestColumns is out of sync with unspentTokenFields: update the fixture and the Scan in dedupedTokenRowsIterator.Next")
}

// tokenRows opens a sqlmock-backed *sql.Rows shaped like the query behind
// dedupedTokenRowsIterator.
func tokenRows(t *testing.T, build func(*sqlmock.Rows) *sqlmock.Rows) *sql.Rows {
	t.Helper()
	db, mockDB, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cols := sqlmock.NewRows(unspentTokenTestColumns)
	mockDB.ExpectQuery("SELECT").WillReturnRows(build(cols))

	rows, err := db.Query("SELECT")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rows.Close() })

	return rows
}

func newDedupedIterator(rows *sql.Rows) *dedupedTokenRowsIterator {
	return &dedupedTokenRowsIterator{rows: rows, seen: make(map[string]struct{})}
}

// A failure part way through iteration must surface as an error. Before this was fixed,
// rows.Next simply returned false and the iterator reported a clean end of results, so callers
// acted on a silently truncated token set.
func TestDedupedTokenRowsIterator_MidIterationErrorSurfaces(t *testing.T) {
	boom := errors.New("connection lost")
	rows := tokenRows(t, func(r *sqlmock.Rows) *sqlmock.Rows {
		return r.
			AddRow("tx1", 0, []byte("owner1"), "TOK", "10", "10", "w1").
			AddRow("tx2", 0, []byte("owner2"), "TOK", "20", "20", "w1").
			RowError(1, boom)
	})
	it := newDedupedIterator(rows)

	first, err := it.Next()
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, "tx1", first.Id.TxId)

	second, err := it.Next()
	require.ErrorIs(t, err, boom)
	require.Nil(t, second)
}

// Exhausting the rows cleanly still reports the end of iteration as (nil, nil).
func TestDedupedTokenRowsIterator_CleanExhaustion(t *testing.T) {
	rows := tokenRows(t, func(r *sqlmock.Rows) *sqlmock.Rows {
		return r.AddRow("tx1", 0, []byte("owner1"), "TOK", "10", "10", "w1")
	})
	it := newDedupedIterator(rows)

	first, err := it.Next()
	require.NoError(t, err)
	require.NotNil(t, first)

	last, err := it.Next()
	require.NoError(t, err)
	require.Nil(t, last)
}

// The dedup key still collapses rows repeated by the UNION ALL branches.
func TestDedupedTokenRowsIterator_DropsDuplicates(t *testing.T) {
	rows := tokenRows(t, func(r *sqlmock.Rows) *sqlmock.Rows {
		return r.
			AddRow("tx1", 0, []byte("owner1"), "TOK", "10", "10", "w1").
			AddRow("tx1", 0, []byte("owner1"), "TOK", "10", "10", "w1")
	})
	it := newDedupedIterator(rows)

	first, err := it.Next()
	require.NoError(t, err)
	require.NotNil(t, first)

	last, err := it.Next()
	require.NoError(t, err)
	require.Nil(t, last, "the repeated row must be collapsed, not yielded twice")
}

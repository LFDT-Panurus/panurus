/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"testing"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestUnspentTokensIteratorByPreparedReuse verifies that repeated calls with
// the same argument shape reuse one prepared statement, and different shapes
// prepare distinct statements (see #1183).
func TestUnspentTokensIteratorByPreparedReuse(t *testing.T) {
	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("test-prepared-reuse"))
	terminate, _, err := fscpostgres.StartPostgres(t.Context(), cfg, nil)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	t.Cleanup(terminate)

	db, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	tables, err := sqlcommon.GetTableNames("")
	require.NoError(t, err)
	store, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)
	require.NoError(t, err)
	require.NoError(t, store.CreateSchema())

	ctx := t.Context()

	// same shape called repeatedly -> statement prepared once and reused
	for range 3 {
		it, err := store.UnspentTokensIteratorBy(ctx, "wallet0", tokentype.Type("GOLD"))
		require.NoError(t, err)
		it.Close()
	}
	require.Equal(t, 1, store.PreparedStmtCount())

	// different values, same shape -> still reused
	it, err := store.UnspentTokensIteratorBy(ctx, "walletX", tokentype.Type("SILVER"))
	require.NoError(t, err)
	it.Close()
	require.Equal(t, 1, store.PreparedStmtCount())

	// different shapes -> distinct statements
	it, err = store.UnspentTokensIteratorBy(ctx, "wallet0", "")
	require.NoError(t, err)
	it.Close()
	it, err = store.UnspentTokensIteratorBy(ctx, "", "")
	require.NoError(t, err)
	it.Close()
	require.Equal(t, 3, store.PreparedStmtCount())
}

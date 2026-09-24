/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package locks

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"
)

// TestNewStores_TableNameParams checks that NewStores forwards Config.TableNameParams
// to GetTableNamesWithConfig, so a deployment that started its Panurus node with a
// non-empty network/channel/namespace identity resolves the same table names here.
// Before this test's corresponding fix, NewStores called GetTableNamesWithConfig with
// zero params regardless of what Config carried (there was no field to carry them at
// all), so the tool would query the wrong (or nonexistent) tables against any real
// deployment using non-empty params.
func TestNewStores_TableNameParams(t *testing.T) {
	const prefix = "pfx"
	params := []string{"net1", "chan1", "ns1"}

	// Compute the table names the exact same way NewStores is expected to: via
	// GetTableNamesWithConfig with the config's prefix, overrides/skip-prefix and
	// params. This is the independent expectation the wiring in stores.go must match.
	storageCfg := sqlcommon.StorageConfig{}
	expected, err := sqlcommon.GetTableNamesWithConfig(prefix, storageCfg, params...)
	require.NoError(t, err)

	dataSource := filepath.Join(t.TempDir(), "test.db")
	seedSQLiteSchema(t, dataSource, expected)

	cfg := Config{
		Driver:          "sqlite",
		DataSource:      dataSource,
		TablePrefix:     prefix,
		TableNameParams: params,
	}

	stores, err := NewStores(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, stores.Close()) }()

	records, err := stores.TokenLock.ListLocks(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "tx1", records[0].TokenID.TxId)
	require.Equal(t, "consumer1", records[0].ConsumerTxID)
	require.NotNil(t, records[0].Status)
	require.Equal(t, driver3.Confirmed, *records[0].Status)
}

// TestNewStores_MissingTableNameParams checks that omitting TableNameParams resolves
// different (here, nonexistent) table names than the ones the schema was seeded
// under, demonstrating that the params genuinely participate in the derived name
// rather than being silently ignored.
func TestNewStores_MissingTableNameParams(t *testing.T) {
	const prefix = "pfx"
	params := []string{"net1", "chan1", "ns1"}

	storageCfg := sqlcommon.StorageConfig{}
	expected, err := sqlcommon.GetTableNamesWithConfig(prefix, storageCfg, params...)
	require.NoError(t, err)

	dataSource := filepath.Join(t.TempDir(), "test.db")
	seedSQLiteSchema(t, dataSource, expected)

	// Same prefix, but no TableNameParams: this must resolve a different table name
	// than the one the schema was created under, so the query fails.
	cfg := Config{
		Driver:      "sqlite",
		DataSource:  dataSource,
		TablePrefix: prefix,
	}

	stores, err := NewStores(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, stores.Close()) }()

	_, err = stores.TokenLock.ListLocks(context.Background())
	require.Error(t, err)
}

// seedSQLiteSchema creates a minimal TokenLocks/Requests schema under the given table
// names and inserts a single held lock whose consuming transaction is Confirmed.
func seedSQLiteSchema(t *testing.T, dataSource string, tableNames sqlcommon.TableNames) {
	t.Helper()

	db, err := sql.Open("sqlite", dataSource)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	_, err = db.Exec(`CREATE TABLE ` + tableNames.Requests + ` (
		tx_id TEXT NOT NULL PRIMARY KEY,
		status INT NOT NULL
	)`)
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE ` + tableNames.TokenLocks + ` (
		tx_id TEXT NOT NULL,
		idx INT NOT NULL,
		consumer_tx_id TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY(tx_id, idx)
	)`)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO `+tableNames.Requests+` (tx_id, status) VALUES (?, ?)`, //nolint:gosec // table name comes from GetTableNamesWithConfig, not attacker input
		"consumer1", driver3.Confirmed)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO `+tableNames.TokenLocks+` (tx_id, idx, consumer_tx_id, created_at) VALUES (?, ?, ?, ?)`, //nolint:gosec // table name comes from GetTableNamesWithConfig, not attacker input
		"tx1", 0, "consumer1", time.Now().Round(0).String())
	require.NoError(t, err)
}

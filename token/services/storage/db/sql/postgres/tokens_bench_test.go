/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package postgres

import (
	"database/sql"
	"testing"

	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

func openBenchTokenStore(b *testing.B) (*sqlcommon.TokenStore, func()) {
	b.Helper()
	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("bench-tokens"))
	terminate, _, err := fscpostgres.StartPostgres(b.Context(), cfg, nil)
	if err != nil {
		b.Skipf("postgres not available: %v", err)
	}
	b.Cleanup(terminate)

	db, err := sql.Open("pgx", cfg.DataSource())
	if err != nil {
		b.Fatal(err)
	}

	tables, err := sqlcommon.GetTableNames("")
	if err != nil {
		b.Fatal(err)
	}
	store, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := store.CreateSchema(); err != nil {
		b.Fatal(err)
	}

	return store, func() { _ = db.Close() }
}

func BenchmarkTokenStore(b *testing.B) {
	store, cleanup := openBenchTokenStore(b)
	defer cleanup()
	sqlcommon.RunTokenStoreBenchmarks(b, store)
}

func BenchmarkTokenStorePreparedComparison(b *testing.B) {
	store, cleanup := openBenchTokenStore(b)
	defer cleanup()
	sqlcommon.RunUnspentTokensIteratorByPreparedComparison(b, store)
}

func BenchmarkSpendableTokensIteratorByPreparedComparison(b *testing.B) {
	store, cleanup := openBenchTokenStore(b)
	defer cleanup()
	sqlcommon.RunSpendableTokensIteratorByPreparedComparison(b, store)
}

func BenchmarkBalancePreparedComparison(b *testing.B) {
	store, cleanup := openBenchTokenStore(b)
	defer cleanup()
	sqlcommon.RunBalancePreparedComparison(b, store)
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"testing"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func openBenchTransactionStore(b *testing.B) (*sqlcommon.TransactionStore, func()) {
	b.Helper()

	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("bench-transactions"))
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

	store, err := sqlcommon.NewTransactionStoreWithNotifierAndRecovery(
		db, db, tables, NewConditionInterpreter(), NewPaginationInterpreter(), nil, NewAdvisoryLockFactoryForID(createTableLockID(tables.Requests+"_recovery")),
	)
	if err != nil {
		b.Fatal(err)
	}
	if err := store.CreateSchema(); err != nil {
		b.Fatal(err)
	}

	return store, func() { _ = db.Close() }
}

func BenchmarkGetStatusPreparedComparison(b *testing.B) {
	store, cleanup := openBenchTransactionStore(b)
	defer cleanup()
	sqlcommon.RunGetStatusPreparedComparison(b, store)
}

func BenchmarkGetTokenRequestPreparedComparison(b *testing.B) {
	store, cleanup := openBenchTransactionStore(b)
	defer cleanup()
	sqlcommon.RunGetTokenRequestPreparedComparison(b, store)
}

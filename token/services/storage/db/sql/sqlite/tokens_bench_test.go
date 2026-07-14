/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package sqlite

import (
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"

	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
)

func openBenchTokenStore(b *testing.B) (*sqlcommon.TokenStore, func()) {
	b.Helper()
	dir := b.TempDir()
	dsn := fmt.Sprintf("file:%s/bench.sqlite?_pragma=busy_timeout(20000)", dir)
	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		b.Fatal(err)
	}
	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		b.Fatal(err)
	}
	tables, err := sqlcommon.GetTableNames("")
	if err != nil {
		b.Fatal(err)
	}
	store, err := sqlcommon.NewTokenStoreWithNotifier(readDB, writeDB, tables, NewConditionInterpreter(), nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := store.CreateSchema(); err != nil {
		b.Fatal(err)
	}

	return store, func() {
		_ = readDB.Close()
		_ = writeDB.Close()
	}
}

func BenchmarkTokenStore(b *testing.B) {
	store, cleanup := openBenchTokenStore(b)
	defer cleanup()
	sqlcommon.RunTokenStoreBenchmarks(b, store)
}

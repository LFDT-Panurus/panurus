/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/dbtest"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	postgres2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// This benchmark exercises the sherdlock selector against a real,
// testcontainers-backed Postgres instance, contending several concurrent
// selections over a deliberately small token pool so that most attempts
// collide on the same rows in TOKEN_LOCKS. It compares the DB-only path
// (localCache=nil, every candidate round-trips to Postgres) against the
// process-local cache path (localCache set, repeat candidates within the
// lease window are rejected without touching the DB), giving a real-DB
// counterpart to the in-memory-locker benchmarks in benchmark_test.go.
const (
	benchWalletID       = "bench-wallet"
	benchTokenType      = token2.Type("BCH")
	benchTokenPoolSize  = 8
	benchSelectQuantity = "1"
)

func BenchmarkSherdlockPostgresContention(b *testing.B) {
	terminate, pgConnStr := startBenchContainer(b)
	defer terminate()

	settings := []struct {
		name       string
		withCache  bool
		numWorkers int
	}{
		{name: "nocache", withCache: false, numWorkers: 8},
		{name: "localcache", withCache: true, numWorkers: 8},
	}

	for _, s := range settings {
		b.Run(s.name, func(b *testing.B) {
			tokenDB, lockDB, cleanup := newBenchTokenAndLockDB(b, pgConnStr)
			defer cleanup()

			seedBenchTokens(b, tokenDB)

			fetcher := NewLazyFetcher(tokenDB)

			var localCache *localLockCache
			leaseExpiry := time.Duration(0)
			if s.withCache {
				localCache = newLocalLockCache()
				leaseExpiry = time.Minute
			}

			cfg := benchmark.NewConfig(s.numWorkers, 3*time.Second, 500*time.Millisecond)
			result := benchmark.RunBenchmark(cfg, func() int { return 0 }, func(int) error {
				return runOneSelection(b, fetcher, lockDB, localCache, leaseExpiry)
			})
			result.Print()
		})
	}
}

// runOneSelection performs one full select-then-unlock cycle against the
// shared Postgres-backed lock/token store, mirroring what StubbornSelector
// does inside the real selection loop.
func runOneSelection(b *testing.B, fetcher TokenFetcher, lockDB Locker, localCache *localLockCache, leaseExpiry time.Duration) error {
	b.Helper()
	txID := transaction.ID(newBenchTxID())
	sel := NewSherdSelector(txID, fetcher, lockDB, localCache, leaseExpiry, testutilsPrecision, time.Millisecond, 20, NewMetrics(&disabled.Provider{}))
	defer func() { _ = sel.UnlockAll(context.Background()) }()

	_, _, err := sel.Select(context.Background(), benchOwnerFilter{}, benchSelectQuantity, benchTokenType)
	if err != nil {
		// Insufficient/locked funds are an expected outcome under heavy
		// contention over a tiny token pool, not a benchmark failure.
		return nil
	}

	return sel.UnlockAll(context.Background())
}

type benchOwnerFilter struct{}

func (benchOwnerFilter) ID() string { return benchWalletID }

const testutilsPrecision = 64

var benchTxCounter int64

func newBenchTxID() string {
	benchTxCounter++

	return fmt.Sprintf("bench-tx-%d", benchTxCounter)
}

func startBenchContainer(b *testing.B) (func(), string) {
	b.Helper()
	cfg := postgres2.DefaultConfig(postgres2.WithDBName("sherdlock-bench"))
	terminate, _, err := postgres2.StartPostgres(b.Context(), cfg, nil)
	if err != nil {
		b.Skipf("postgres not available: %v", err)
	}

	return terminate, cfg.DataSource()
}

type benchDBProvider struct{}

func (p *benchDBProvider) Get(opts postgres2.Opts) (*common.RWDB, error) { return postgres2.Open(opts) }

func newBenchTokenAndLockDB(b *testing.B, pgConnStr string) (dbtest.TestTokenDB, dbdriver.TokenLockStore, func()) {
	b.Helper()
	d := postgres.NewDriverWithDbProvider(multiplexed.MockTypeConfig(postgres2.Persistence, postgres2.Config{
		TablePrefix:  "bench",
		DataSource:   pgConnStr,
		MaxOpenConns: 20,
	}), &benchDBProvider{})

	tokenDB, err := d.NewToken("")
	if err != nil {
		b.Fatal(err)
	}
	lockDB, err := d.NewTokenLock("")
	if err != nil {
		b.Fatal(err)
	}

	return tokenDB.(dbtest.TestTokenDB), lockDB, func() {
		_ = lockDB.Close()
		_ = tokenDB.Close()
	}
}

// seedBenchTokens stores a small, fixed pool of spendable tokens so that
// concurrent selections are forced to contend on the same rows.
func seedBenchTokens(b *testing.B, tokenDB dbtest.TestTokenDB) {
	b.Helper()
	ctx := context.Background()
	for i := range benchTokenPoolSize {
		q := token2.NewOneQuantity(testutilsPrecision)
		rec := dbdriver.TokenRecord{
			TxID:           fmt.Sprintf("bench-seed-%d", i),
			Index:          0,
			OwnerRaw:       []byte(benchWalletID),
			OwnerType:      "idemix",
			OwnerIdentity:  []byte(benchWalletID),
			OwnerWalletID:  benchWalletID,
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte("meta"),
			Quantity:       q.Hex(),
			Type:           benchTokenType,
			Owner:          true,
		}
		if err := tokenDB.StoreToken(ctx, rec, []string{benchWalletID}); err != nil {
			b.Fatalf("failed to seed bench token %d: %v", i, err)
		}
	}
}

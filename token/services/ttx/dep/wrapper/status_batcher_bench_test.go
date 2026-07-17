/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
)

const benchSeededTxs = 1024

// openBenchTransactionsStore opens a file-backed sqlite transactions store
// seeded with benchSeededTxs token requests, mirroring the setup of
// sqlite's tokens_bench_test.go.
func openBenchTransactionsStore(b *testing.B) *sqlcommon.TransactionStore {
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
	b.Cleanup(func() {
		_ = readDB.Close()
		_ = writeDB.Close()
	})
	tables, err := sqlcommon.GetTableNames("")
	if err != nil {
		b.Fatal(err)
	}
	store, err := sqlcommon.NewOwnerTransactionStore(readDB, writeDB, tables, sqlite.NewConditionInterpreter(), sqlite.NewPaginationInterpreter())
	if err != nil {
		b.Fatal(err)
	}
	if err := store.CreateSchema(); err != nil {
		b.Fatal(err)
	}

	w, err := store.NewTransactionStoreTransaction()
	if err != nil {
		b.Fatal(err)
	}
	for i := range benchSeededTxs {
		if err := w.AddTokenRequest(context.Background(), benchTxID(i), []byte("request"), nil, nil, tdriver.PPHash("pp")); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		b.Fatal(err)
	}

	return store
}

func benchTxID(i int) string {
	return fmt.Sprintf("tx-%04d", i%benchSeededTxs)
}

// delayedStore wraps a store and adds a fixed latency to every query, to
// model the network round trip of a remote database (sqlite is in-process,
// so its per-query cost is a few tens of microseconds; a remote postgres
// adds an RTT on the order of 0.5-2ms per query). The optional pool channel
// bounds in-flight queries the way a real connection pool does — without
// it, unlimited parallel sleeps would let the unbatched path scale far
// beyond what a real database allows. It also counts queries.
type delayedStore struct {
	store   *sqlcommon.TransactionStore
	delay   time.Duration
	pool    chan struct{}
	queries atomic.Int64
}

func newDelayedStore(store *sqlcommon.TransactionStore, delay time.Duration, poolSize int) *delayedStore {
	d := &delayedStore{store: store, delay: delay}
	if poolSize > 0 {
		d.pool = make(chan struct{}, poolSize)
	}

	return d
}

func (d *delayedStore) query() func() {
	d.queries.Add(1)
	if d.pool != nil {
		d.pool <- struct{}{}
	}
	if d.delay > 0 {
		time.Sleep(d.delay)
	}

	return func() {
		if d.pool != nil {
			<-d.pool
		}
	}
}

func (d *delayedStore) GetStatus(ctx context.Context, txID string) (dbdriver.TxStatus, string, error) {
	defer d.query()()

	return d.store.GetStatus(ctx, txID)
}

func (d *delayedStore) GetStatuses(ctx context.Context, txIDs []string) (map[string]dbdriver.TxStatus, error) {
	defer d.query()()

	return d.store.GetStatuses(ctx, txIDs)
}

// runConcurrentLookups distributes b.N lookups across the given number of
// worker goroutines, modelling concurrent finality views, and reports the
// number of DB queries issued per lookup.
func runConcurrentLookups(b *testing.B, workers int, queries *atomic.Int64, lookup func(txID string) error) {
	b.Helper()
	queries.Store(0)
	var next atomic.Int64
	var failed atomic.Int64
	b.ResetTimer()

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := next.Add(1) - 1
				if i >= int64(b.N) {
					return
				}
				if err := lookup(benchTxID(int(i))); err != nil {
					failed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	b.StopTimer()

	if failed.Load() > 0 {
		b.Fatalf("%d lookups failed", failed.Load())
	}
	b.ReportMetric(float64(queries.Load())/float64(b.N), "queries/op")
}

// BenchmarkStatusLookups compares direct per-tx GetStatus calls against the
// batching decorator path, across concurrency levels and two DB profiles:
// in-process sqlite as-is, and a simulated remote DB (1ms round trip,
// 16-connection pool).
func BenchmarkStatusLookups(b *testing.B) {
	profiles := []struct {
		name  string
		delay time.Duration
		pool  int
	}{
		{name: "sqlite", delay: 0, pool: 0},
		{name: "remote-rtt=1ms-pool=16", delay: time.Millisecond, pool: 16},
	}
	for _, profile := range profiles {
		for _, workers := range []int{1, 8, 32, 128} {
			suffix := fmt.Sprintf("%s/workers=%d", profile.name, workers)

			b.Run("direct/"+suffix, func(b *testing.B) {
				ds := newDelayedStore(openBenchTransactionsStore(b), profile.delay, profile.pool)
				runConcurrentLookups(b, workers, &ds.queries, func(txID string) error {
					_, _, err := ds.GetStatus(context.Background(), txID)

					return err
				})
			})

			b.Run("batched/"+suffix, func(b *testing.B) {
				ds := newDelayedStore(openBenchTransactionsStore(b), profile.delay, profile.pool)
				batcher := newStatusBatcher(ds)
				runConcurrentLookups(b, workers, &ds.queries, func(txID string) error {
					_, err := batcher.Get(context.Background(), txID)

					return err
				})
			})
		}
	}
}

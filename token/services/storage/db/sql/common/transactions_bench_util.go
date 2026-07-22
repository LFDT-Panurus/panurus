/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/benchmark"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	q "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/query/cond"
)

// seedBenchRequest inserts a single request row for txID with status Pending,
// so GetStatus has something real to read during the benchmark.
func seedBenchRequest(b *testing.B, store *TransactionStore, txID string) {
	b.Helper()

	tx, err := store.NewTransactionStoreTransaction()
	if err != nil {
		b.Fatalf("failed starting tx: %v", err)
	}
	if err := tx.AddTokenRequest(context.Background(), txID, []byte("request-bytes"), nil, nil, []byte("pp-hash")); err != nil {
		b.Fatalf("failed seeding request: %v", err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("failed committing seed tx: %v", err)
	}
}

// RunGetStatusPreparedComparison benchmarks GetStatus against a version of
// the same query executed via a statement prepared once outside the timed
// loop, using the same worker count and duration as RunTokenStoreBenchmarks
// so the numbers are directly comparable.
//
// Unlike the token-store queries, GetStatus has exactly one query shape
// (tx_id = ?, always present) - this benchmark exists to confirm the win
// holds for that simpler, single-shape case, backing the finality-polling
// loop in ttx/finality.go.
// RunGetTokenRequestPreparedComparison benchmarks GetTokenRequest against a
// version of the same query executed via a statement prepared once outside
// the timed loop. Same single-shape reasoning as GetStatus.
func RunGetTokenRequestPreparedComparison(b *testing.B, store *TransactionStore) {
	b.Helper()

	b.Run("GetTokenRequest_Dynamic", func(b *testing.B) {
		txID := fmt.Sprintf("bench-tx-req-dynamic-%d", time.Now().UnixNano())
		seedBenchRequest(b, store, txID)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TransactionStore { return store },
			func(s *TransactionStore) error {
				_, err := s.GetTokenRequest(context.Background(), txID)

				return err
			},
		)
		result.Print()
	})

	b.Run("GetTokenRequest_Prepared", func(b *testing.B) {
		txID := fmt.Sprintf("bench-tx-req-prepared-%d", time.Now().UnixNano())
		seedBenchRequest(b, store, txID)

		query, args := q.Select().
			FieldsByName("request").
			From(q.Table(store.table.Requests)).
			Where(cond.Eq("tx_id", txID)).
			Format(store.ci)

		stmt, err := store.readDB.PrepareContext(context.Background(), query)
		if err != nil {
			b.Fatalf("failed preparing statement: %v", err)
		}
		defer func() { _ = stmt.Close() }()

		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *sql.Stmt { return stmt },
			func(s *sql.Stmt) error {
				var request []byte

				return s.QueryRowContext(context.Background(), args...).Scan(&request)
			},
		)
		result.Print()
	})
}

func RunGetStatusPreparedComparison(b *testing.B, store *TransactionStore) {
	b.Helper()

	b.Run("GetStatus_Dynamic", func(b *testing.B) {
		txID := fmt.Sprintf("bench-tx-dynamic-%d", time.Now().UnixNano())
		seedBenchRequest(b, store, txID)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TransactionStore { return store },
			func(s *TransactionStore) error {
				_, _, err := s.GetStatus(context.Background(), txID)

				return err
			},
		)
		result.Print()
	})

	b.Run("GetStatus_Prepared", func(b *testing.B) {
		txID := fmt.Sprintf("bench-tx-prepared-%d", time.Now().UnixNano())
		seedBenchRequest(b, store, txID)

		query, args := q.Select().
			FieldsByName("status", "status_message").
			From(q.Table(store.table.Requests)).
			Where(cond.Eq("tx_id", txID)).
			Format(store.ci)

		stmt, err := store.readDB.PrepareContext(context.Background(), query)
		if err != nil {
			b.Fatalf("failed preparing statement: %v", err)
		}
		defer func() { _ = stmt.Close() }()

		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *sql.Stmt { return stmt },
			func(s *sql.Stmt) error {
				var status dbdriver.TxStatus
				var statusMessage string

				return s.QueryRowContext(context.Background(), args...).Scan(&status, &statusMessage)
			},
		)
		result.Print()
	})
}

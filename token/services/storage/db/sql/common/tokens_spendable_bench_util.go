/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/benchmark"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
)

// RunSpendableTokensIteratorByPreparedComparison benchmarks
// SpendableTokensIteratorBy against a version of the same query executed
// via a statement prepared once outside the timed loop, using the same
// seed data, worker count, and duration as RunTokenStoreBenchmarks so the
// numbers are directly comparable.
//
// This exists to answer: "how much would moving to prepared statements
// help for the selector's spend-fetch query?" (see #1919). The query built
// by SpendableTokensIteratorBy is dynamic (its shape depends on whether
// walletID / typ are empty), so this comparison fixes both parameters to
// non-empty values matching the seeded data, which is the common case in
// production (see token/services/selector/sherdlock/fetcher.go).
func RunSpendableTokensIteratorByPreparedComparison(b *testing.B, store *TokenStore) {
	b.Helper()

	const walletID = "wallet0"
	tokenType := tokentype.Type("GOLD")

	b.Run("SpendableTokensIteratorBy_Dynamic", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TokenStore { return store },
			func(s *TokenStore) error {
				it, err := s.SpendableTokensIteratorBy(context.Background(), walletID, tokenType)
				if err != nil {
					return err
				}
				defer it.Close()
				for {
					tok, err := it.Next()
					if err != nil {
						return err
					}
					if tok == nil {
						break
					}
				}

				return nil
			},
		)
		result.Print()
	})

	b.Run("SpendableTokensIteratorBy_Prepared", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)

		query, args := buildSpendableTokensIteratorByQuery(store, walletID, tokenType)

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
				rows, err := s.QueryContext(context.Background(), args...)
				if err != nil {
					return err
				}
				defer func() { _ = rows.Close() }()

				for rows.Next() {
					var r struct {
						TxID          string
						Index         uint64
						Type          tokentype.Type
						Quantity      string
						OwnerWalletID sql.NullString
					}
					if err := rows.Scan(&r.TxID, &r.Index, &r.Type, &r.Quantity, &r.OwnerWalletID); err != nil {
						return err
					}
				}

				return rows.Err()
			},
		)
		result.Print()
	})
}

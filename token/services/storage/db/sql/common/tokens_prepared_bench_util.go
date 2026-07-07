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

// RunUnspentTokensIteratorByPreparedComparison benchmarks UnspentTokensIteratorBy
// against a version of the same query executed via a statement prepared once
// outside the timed loop, using the same seed data, worker count, and duration
// as RunTokenStoreBenchmarks so the numbers are directly comparable.
//
// This exists to answer: "how much would moving to prepared statements help
// for our primary token-selection query?" (see #1183). The query built by
// UnspentTokensIteratorBy is dynamic (its shape depends on whether walletID /
// tokenType are empty), so this comparison fixes both parameters to non-empty
// values matching the seeded data, which is the common case in production.
func RunUnspentTokensIteratorByPreparedComparison(b *testing.B, store *TokenStore) {
	b.Helper()

	const walletID = "wallet0"
	tokenType := tokentype.Type("GOLD")

	b.Run("UnspentTokensIteratorBy_Dynamic", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TokenStore { return store },
			func(s *TokenStore) error {
				it, err := s.UnspentTokensIteratorBy(context.Background(), walletID, tokenType)
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

	b.Run("UnspentTokensIteratorBy_Prepared", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)

		query, args := buildUnspentTokensIteratorByQuery(store, walletID, tokenType)

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

				it := &dedupedTokenRowsIterator{rows: rows, seen: make(map[string]struct{})}
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
}

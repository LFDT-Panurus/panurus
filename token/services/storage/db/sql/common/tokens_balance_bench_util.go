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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
)

// RunBalancePreparedComparison benchmarks balance against a version of the
// same query executed via a statement prepared once outside the timed loop,
// using the same seed data, worker count, and duration as
// RunTokenStoreBenchmarks so the numbers are directly comparable.
//
// This exists to answer: "how much would moving to prepared statements help
// for wallet balance checks?" balance() has a single caller (Balance) that
// always sets only WalletID/TokenType, matching the same shape as
// UnspentTokensIteratorBy/SpendableTokensIteratorBy.
func RunBalancePreparedComparison(b *testing.B, store *TokenStore) {
	b.Helper()

	const walletID = "wallet0"
	tokenType := tokentype.Type("GOLD")
	opts := driver.QueryTokenDetailsParams{WalletID: walletID, TokenType: tokenType}

	b.Run("Balance_Dynamic", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TokenStore { return store },
			func(s *TokenStore) error {
				_, err := s.Balance(context.Background(), walletID, tokenType)

				return err
			},
		)
		result.Print()
	})

	b.Run("Balance_Prepared", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)

		query, args := buildBalanceQuery(store, opts)

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
				var sum BigInt

				return s.QueryRowContext(context.Background(), args...).Scan(&sum)
			},
		)
		result.Print()
	})
}

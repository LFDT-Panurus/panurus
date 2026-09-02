/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package common

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	tokentype "github.com/LFDT-Panurus/panurus/token/token"
)

func SeedBenchTokens(b *testing.B, store *TokenStore, n int) {
	b.Helper()
	ctx := context.Background()
	for i := range n {
		rec := driver.TokenRecord{
			TxID:           fmt.Sprintf("tx%d", i),
			Index:          0,
			OwnerRaw:       []byte("wallet0"),
			OwnerType:      "idemix",
			OwnerIdentity:  []byte("wallet0"),
			OwnerWalletID:  "wallet0",
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte("meta"),
			Quantity:       "0x64",
			Type:           tokentype.Type("GOLD"),
		}
		if err := store.StoreToken(ctx, rec, []string{"wallet0"}); err != nil {
			b.Fatalf("seed failed at i=%d: %v", i, err)
		}
	}
}

func RunTokenStoreBenchmarks(b *testing.B, store *TokenStore) {
	b.Helper()
	b.Run("UnspentTokensIterator", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TokenStore { return store },
			func(s *TokenStore) error {
				it, err := s.UnspentTokensIterator(context.Background())
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

	b.Run("Balance", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TokenStore { return store },
			func(s *TokenStore) error {
				_, err := s.Balance(context.Background(), "wallet0", tokentype.Type("GOLD"))

				return err
			},
		)
		result.Print()
	})

	b.Run("UnspentTokensIteratorBy", func(b *testing.B) {
		SeedBenchTokens(b, store, 1000)
		cfg := benchmark.NewConfig(4, 5*time.Second, 500*time.Millisecond)
		result := benchmark.RunBenchmark(
			cfg,
			func() *TokenStore { return store },
			func(s *TokenStore) error {
				it, err := s.UnspentTokensIteratorBy(context.Background(), "wallet0", tokentype.Type("GOLD"), 0)
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
}

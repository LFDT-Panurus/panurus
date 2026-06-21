/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func TestLocker_QuotaEnforcement(t *testing.T) {
	t.Run("enforces per-identity quota", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 5,
			RateLimit:           0, // disable rate limiting for this test
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"
		txID := "tx1"

		// Should allow up to quota
		for i := range 5 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, txID, false)
			require.NoError(t, err, "lock %d should succeed", i)
		}

		// Next lock should fail due to quota
		tokenID := &token2.ID{TxId: "token", Index: 5}
		_, err := locker.Lock(ctx, identity, tokenID, txID, false)
		require.ErrorIs(t, err, simple.ErrQuotaExceeded)
	})

	t.Run("quota is per-identity", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 3,
			RateLimit:           0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity1 := "wallet1"
		identity2 := "wallet2"

		// Lock 3 tokens for identity1
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token1", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity1, tokenID, "tx1", false)
			require.NoError(t, err)
		}

		// identity1 should be at quota
		tokenID := &token2.ID{TxId: "token1", Index: 3}
		_, err := locker.Lock(ctx, identity1, tokenID, "tx1", false)
		require.ErrorIs(t, err, simple.ErrQuotaExceeded)

		// identity2 should still have full quota
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token2", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity2, tokenID, "tx2", false)
			require.NoError(t, err, "identity2 lock %d should succeed", i)
		}
	})

	t.Run("quota decreases on unlock", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 3,
			RateLimit:           0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"

		// Lock 3 tokens
		var tokenIDs []*token2.ID
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			tokenIDs = append(tokenIDs, tokenID)
			_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
			require.NoError(t, err)
		}

		// At quota
		tokenID := &token2.ID{TxId: "token", Index: 3}
		_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
		require.ErrorIs(t, err, simple.ErrQuotaExceeded)

		// Unlock 2 tokens
		locker.UnlockIDs(ctx, identity, tokenIDs[0], tokenIDs[1])

		// Should now be able to lock 2 more
		for i := 3; i < 5; i++ {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
			require.NoError(t, err, "lock after unlock %d should succeed", i)
		}
	})

	t.Run("quota decreases on unlock by txID", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 3,
			RateLimit:           0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"
		txID := "tx1"

		// Lock 3 tokens
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, txID, false)
			require.NoError(t, err)
		}

		// At quota
		tokenID := &token2.ID{TxId: "token", Index: 3}
		_, err := locker.Lock(ctx, identity, tokenID, txID, false)
		require.ErrorIs(t, err, simple.ErrQuotaExceeded)

		// Unlock by txID
		locker.UnlockByTxID(ctx, txID)

		// Should now have full quota again
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token2", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, "tx2", false)
			require.NoError(t, err, "lock after unlock by txID %d should succeed", i)
		}
	})

	t.Run("quota with empty identity is not enforced", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 3,
			RateLimit:           0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()

		// Should allow more than quota when identity is empty
		for i := range 10 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, "", tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed with empty identity", i)
		}
	})

	t.Run("quota disabled when set to 0", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 0, // disabled
			RateLimit:           0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"

		// Should allow unlimited locks
		for i := range 100 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed with quota disabled", i)
		}
	})
}

func TestLocker_RateLimiting(t *testing.T) {
	t.Run("enforces rate limit", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 0, // disable quota
			RateLimit:           5.0,
			RateLimitBurst:      5.0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"

		// Should allow burst
		for i := range 5 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed within burst", i)
		}

		// Next should be rate limited
		tokenID := &token2.ID{TxId: "token", Index: 5}
		_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)
	})

	t.Run("rate limit is per-identity", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 0,
			RateLimit:           3.0,
			RateLimitBurst:      3.0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity1 := "wallet1"
		identity2 := "wallet2"

		// Exhaust identity1's rate limit
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token1", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity1, tokenID, "tx1", false)
			require.NoError(t, err)
		}

		// identity1 should be rate limited
		tokenID := &token2.ID{TxId: "token1", Index: 3}
		_, err := locker.Lock(ctx, identity1, tokenID, "tx1", false)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)

		// identity2 should still have full rate limit
		for i := range 3 {
			tokenID := &token2.ID{TxId: "token2", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity2, tokenID, "tx2", false)
			require.NoError(t, err, "identity2 lock %d should succeed", i)
		}
	})

	t.Run("rate limit with empty identity is not enforced", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 0,
			RateLimit:           3.0,
			RateLimitBurst:      3.0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()

		// Should allow more than rate limit when identity is empty
		for i := range 10 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, "", tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed with empty identity", i)
		}
	})

	t.Run("rate limit disabled when set to 0", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 0,
			RateLimit:           0, // disabled
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"

		// Should allow unlimited locks
		for i := range 100 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed with rate limit disabled", i)
		}
	})
}

func TestLocker_QuotaAndRateLimitCombined(t *testing.T) {
	t.Run("both quota and rate limit enforced", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 10,
			RateLimit:           5.0,
			RateLimitBurst:      5.0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()
		identity := "wallet1"

		// Should allow burst (5 locks)
		for i := range 5 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed", i)
		}

		// Next should be rate limited (not quota, since we're at 5/10)
		tokenID := &token2.ID{TxId: "token", Index: 5}
		_, err := locker.Lock(ctx, identity, tokenID, "tx1", false)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)
	})

	t.Run("backward compatibility: empty owner bypasses quota and rate limit", func(t *testing.T) {
		config := LockerConfig{
			MaxLocksPerIdentity: 5,
			RateLimit:           10.0,
			RateLimitBurst:      10.0,
		}
		locker := NewLockerWithConfig(newMockTXStatusProvider(), 0, 0, config).(*locker)
		defer func() { _ = locker.Stop() }()

		ctx := context.Background()

		// Empty owner should bypass identity tracking
		for i := range 20 {
			tokenID := &token2.ID{TxId: "token", Index: uint64(i)}
			_, err := locker.Lock(ctx, "", tokenID, "tx1", false)
			require.NoError(t, err, "lock %d should succeed without identity", i)
		}
	})
}

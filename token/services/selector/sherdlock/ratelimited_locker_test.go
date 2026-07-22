/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingLocker is a minimal sherdlock.Locker that counts Lock calls.
type countingLocker struct{ calls int }

func (c *countingLocker) Lock(_ context.Context, _ *token2.ID, _ transaction.ID, _ string) error {
	c.calls++

	return nil
}
func (c *countingLocker) UnlockByTxID(_ context.Context, _ transaction.ID) error { return nil }
func (c *countingLocker) Cleanup(_ context.Context, _ time.Duration) error       { return nil }

func TestSherdlockRateLimitedLocker_NilLimiterIsPassThrough(t *testing.T) {
	inner := &countingLocker{}
	got := NewRateLimitedLocker(inner, nil)
	assert.Same(t, inner, got, "nil limiter must return the inner locker unchanged")
}

func TestSherdlockRateLimitedLocker_DeniesWithSentinel(t *testing.T) {
	inner := &countingLocker{}
	limiter := ratelimit.NewTokenBucketRateLimiter(2, 2, 0, 0)
	defer limiter.Stop()

	l := NewRateLimitedLocker(inner, limiter)
	id := &token2.ID{TxId: "tx", Index: 0}

	for i := range 2 {
		require.NoError(t, l.Lock(context.Background(), id, "txID", "wallet1"), "request %d within burst should pass", i)
	}
	assert.Equal(t, 2, inner.calls)

	err := l.Lock(context.Background(), id, "txID", "wallet1")
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Equal(t, 2, inner.calls, "throttled lock must not reach the inner store")
}

func TestSherdlockRateLimitedLocker_EmptyWalletBypasses(t *testing.T) {
	inner := &countingLocker{}
	limiter := ratelimit.NewTokenBucketRateLimiter(1, 1, 0, 0)
	defer limiter.Stop()

	l := NewRateLimitedLocker(inner, limiter)
	id := &token2.ID{TxId: "tx", Index: 0}

	for range 5 {
		require.NoError(t, l.Lock(context.Background(), id, "txID", ""))
	}
	assert.Equal(t, 5, inner.calls)
}

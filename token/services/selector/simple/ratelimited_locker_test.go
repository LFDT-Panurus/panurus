/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitedLocker_NilLimiterIsPassThrough(t *testing.T) {
	inner := &recordingLocker{lockFailAfter: 1_000_000}
	got := NewRateLimitedLocker(inner, nil)
	assert.Same(t, inner, got, "nil limiter must return the inner locker unchanged")
}

func TestRateLimitedLocker_DeniesWithSentinel(t *testing.T) {
	inner := &recordingLocker{lockFailAfter: 1_000_000} // inner always succeeds
	limiter := ratelimit.NewTokenBucketRateLimiter(2, 2, 0, 0)
	defer limiter.Stop()

	l := NewRateLimitedLocker(inner, limiter)
	id := &token2.ID{TxId: "tx", Index: 0}

	// Burst of 2 is allowed and reaches the inner locker.
	for i := range 2 {
		_, err := l.Lock(context.Background(), id, "txID", "wallet1", false)
		require.NoError(t, err, "request %d within burst should pass", i)
	}
	assert.Equal(t, 2, inner.calls, "both allowed locks reached the inner locker")

	// Next one is throttled: the sentinel is returned and the inner locker is not called.
	_, err := l.Lock(context.Background(), id, "txID", "wallet1", false)
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Equal(t, 2, inner.calls, "throttled lock must not reach the inner locker")
}

func TestRateLimitedLocker_EmptyWalletBypasses(t *testing.T) {
	inner := &recordingLocker{lockFailAfter: 1_000_000}
	limiter := ratelimit.NewTokenBucketRateLimiter(1, 1, 0, 0)
	defer limiter.Stop()

	l := NewRateLimitedLocker(inner, limiter)
	id := &token2.ID{TxId: "tx", Index: 0}

	// An empty wallet id means "no policy": never throttled.
	for range 5 {
		_, err := l.Lock(context.Background(), id, "txID", "", false)
		require.NoError(t, err)
	}
	assert.Equal(t, 5, inner.calls)
}

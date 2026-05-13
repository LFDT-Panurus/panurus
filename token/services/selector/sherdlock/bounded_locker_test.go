/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tokenID(txID string, idx uint64) *token2.ID {
	return &token2.ID{TxId: txID, Index: idx}
}

// newFakeLocker returns a FakeLocker configured to succeed on TryLock and Lock.
func newFakeLocker() *mocks.FakeLocker {
	l := &mocks.FakeLocker{}
	l.LockReturns(nil)
	l.UnlockByTxIDReturns(nil)
	l.CleanupReturns(nil)

	return l
}

func TestNewBoundedLocker_ZeroLimit_ReturnsInner(t *testing.T) {
	inner := newFakeLocker()
	got := sherdlock.NewBoundedLocker(inner, 0)
	assert.Equal(t, inner, got, "zero limit should return inner locker unchanged")
}

func TestNewBoundedLocker_NegativeLimit_ReturnsInner(t *testing.T) {
	inner := newFakeLocker()
	got := sherdlock.NewBoundedLocker(inner, -1)
	assert.Equal(t, inner, got, "negative limit should return inner locker unchanged")
}

func TestBoundedLocker_UnderLimit_Succeeds(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 3)

	ctx := t.Context()
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 2), "consumer1", "wallet1"))

	assert.Equal(t, 3, inner.LockCallCount())
}

func TestBoundedLocker_AtLimit_ReturnsRateLimited(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 2)

	ctx := t.Context()
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))

	err := bl.Lock(ctx, tokenID("tx1", 2), "consumer1", "wallet1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, token.SelectorRateLimited), "expected SelectorRateLimited, got: %v", err)
	// inner must not have been called for the 3rd lock
	assert.Equal(t, 2, inner.LockCallCount())
}

func TestBoundedLocker_LimitIsPerTx(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 2)

	ctx := t.Context()
	// Two different consumer transactions share the limit independently.
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx2", 0), "consumer2", "wallet2"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx2", 1), "consumer2", "wallet2"))
	assert.Equal(t, 4, inner.LockCallCount())
}

func TestBoundedLocker_UnlockByTxID_ResetsCounter(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 2)

	ctx := t.Context()
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))

	// limit reached
	err := bl.Lock(ctx, tokenID("tx1", 2), "consumer1", "wallet1")
	require.True(t, errors.Is(err, token.SelectorRateLimited))

	// unlock clears the counter for consumer1
	require.NoError(t, bl.UnlockByTxID(ctx, "consumer1"))
	assert.Equal(t, 1, inner.UnlockByTxIDCallCount())

	// can acquire locks again
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))
}

// txScopedState mirrors the unexported interface Manager uses to drive
// replica-local per-transaction bookkeeping on the locker.
type txScopedState interface {
	ForgetTx(txID transaction.ID)
	EvictStaleTxState(olderThan time.Duration)
}

// TestBoundedLocker_Cleanup_KeepsLiveCounters pins that Cleanup does not reset
// the per-transaction counters. Wiping them would make the ceiling "per
// transaction per cleanup tick": an in-flight transaction already holding its
// full quota would be handed a fresh budget on the next tick (one minute by
// default) and could acquire maxLocksPerTx more, without bound.
func TestBoundedLocker_Cleanup_KeepsLiveCounters(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 1)

	ctx := t.Context()
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))

	err := bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1")
	require.True(t, errors.Is(err, token.SelectorRateLimited))

	require.NoError(t, bl.Cleanup(ctx, time.Minute))
	assert.Equal(t, 1, inner.CleanupCallCount(), "Cleanup must still reach the inner locker")

	// The transaction is still holding its quota, so the ceiling still binds.
	err = bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, token.SelectorRateLimited),
		"ceiling must survive a cleanup tick, got: %v", err)
}

// TestBoundedLocker_EvictStaleTxState verifies the backstop that reclaims
// counters for transactions that were never closed: entries idle longer than
// the lease window go away, recent ones stay.
func TestBoundedLocker_EvictStaleTxState(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 1)
	state, ok := bl.(txScopedState)
	require.True(t, ok, "boundedLocker must expose per-transaction state to Manager")

	ctx := t.Context()
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))

	// A recent reservation is not stale, so the ceiling still binds.
	state.EvictStaleTxState(time.Minute)
	err := bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, token.SelectorRateLimited))

	// With a zero window every existing entry is past its cutoff and is
	// reclaimed, so the id starts over with a full budget.
	state.EvictStaleTxState(0)
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))
}

// TestBoundedLocker_ForgetTx verifies the counter is released when a selector is
// closed, without unlocking the tokens: after a successful selection the
// transaction still needs its locks, but nothing else would reclaim the counter
// on a replica that never wins cleanup leadership.
func TestBoundedLocker_ForgetTx(t *testing.T) {
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, 1)
	state, ok := bl.(txScopedState)
	require.True(t, ok)

	ctx := t.Context()
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1"))
	require.Error(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))

	state.ForgetTx("consumer1")

	assert.Zero(t, inner.UnlockByTxIDCallCount(), "ForgetTx must not release the tokens")
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))
}

func TestBoundedLocker_InnerLockError_DoesNotIncrementCounter(t *testing.T) {
	inner := newFakeLocker()
	inner.LockReturnsOnCall(0, errors.New("db error"))
	bl := sherdlock.NewBoundedLocker(inner, 2)

	ctx := t.Context()
	err := bl.Lock(ctx, tokenID("tx1", 0), "consumer1", "wallet1")
	require.Error(t, err)
	assert.False(t, errors.Is(err, token.SelectorRateLimited))

	// second attempt must still succeed (counter was not incremented)
	require.NoError(t, bl.Lock(ctx, tokenID("tx1", 1), "consumer1", "wallet1"))
}

func TestBoundedLocker_Concurrent(t *testing.T) {
	const limit = 5
	inner := newFakeLocker()
	bl := sherdlock.NewBoundedLocker(inner, limit)

	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			idx := uint64(i) //nolint:gosec // i is in [0,19], conversion is safe
			errs[i] = bl.Lock(ctx, tokenID("tx1", idx), "consumer1", "wallet1")
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, e := range errs {
		if e == nil {
			successes++
		}
	}
	assert.Equal(t, limit, successes, "exactly limit locks should succeed")
}

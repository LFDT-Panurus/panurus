/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"testing"
	"time"

	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalLockCache_RecordAndLockedBy(t *testing.T) {
	c := newLocalLockCache()
	id := token2.ID{TxId: "tx1", Index: 0}

	_, fresh := c.lockedBy(id, time.Minute)
	require.False(t, fresh, "an unseen token must report not-fresh")

	c.record(id, transaction.ID("txA"))
	owner, fresh := c.lockedBy(id, time.Minute)
	require.True(t, fresh)
	assert.Equal(t, transaction.ID("txA"), owner)
}

func TestLocalLockCache_StaleEntryIgnored(t *testing.T) {
	c := newLocalLockCache()
	id := token2.ID{TxId: "tx1", Index: 0}
	c.record(id, transaction.ID("txA"))

	// leaseExpiry <= 0 means entries never expire.
	_, fresh := c.lockedBy(id, 0)
	require.True(t, fresh)

	time.Sleep(2 * time.Millisecond)
	_, fresh = c.lockedBy(id, time.Millisecond)
	require.False(t, fresh, "an entry older than leaseExpiry must be treated as stale")
}

func TestLocalLockCache_ReleaseOwned(t *testing.T) {
	c := newLocalLockCache()
	id1 := token2.ID{TxId: "tx1", Index: 0}
	id2 := token2.ID{TxId: "tx2", Index: 0}
	c.record(id1, transaction.ID("txA"))
	c.record(id2, transaction.ID("txB"))

	c.releaseOwned(transaction.ID("txA"))

	_, fresh := c.lockedBy(id1, time.Minute)
	assert.False(t, fresh, "txA's entry must be gone after releaseOwned")
	_, fresh = c.lockedBy(id2, time.Minute)
	assert.True(t, fresh, "txB's entry must be untouched")
}

func TestLocalLockCache_SweepExpired(t *testing.T) {
	c := newLocalLockCache()
	id := token2.ID{TxId: "tx1", Index: 0}
	c.record(id, transaction.ID("txA"))

	time.Sleep(2 * time.Millisecond)
	c.sweepExpired(time.Millisecond)

	_, ok := c.entries.Load(id)
	assert.False(t, ok, "sweepExpired must delete stale entries")
}

// TestLocker_DedupSameTokenTwiceWithinOneSelection reproduces the literal
// issue case: a selection loop that encounters the same candidate token
// twice (e.g. a duplicate row from the DB iterator) must not round-trip to
// the DB twice - the second TryLock is answered entirely from the local
// cache, exactly as a real PK-violation would answer it.
func TestLocker_DedupSameTokenTwiceWithinOneSelection(t *testing.T) {
	id := token2.ID{TxId: "tx1", Index: 0}
	var lockCalls int
	base := &mockLocker{lockFunc: func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error {
		lockCalls++

		return nil
	}}
	l := &locker{Locker: base, txID: transaction.ID("txA"), localCache: newLocalLockCache(), leaseExpiry: time.Minute}

	require.True(t, l.TryLock(t.Context(), &id))
	require.False(t, l.TryLock(t.Context(), &id), "second attempt on the same token within the same selection must be rejected locally")
	assert.Equal(t, 1, lockCalls, "the underlying Lock must be called only once")
}

// TestLocker_CrossSelectorSameProcessDedup reproduces the cross-transaction
// case raised in PR #1483's review comment: two different selectors
// (different consumer tx IDs) in the same process share one Manager's
// localCache, so the second selector's attempt to lock a token already
// claimed by the first is rejected without a DB round trip.
func TestLocker_CrossSelectorSameProcessDedup(t *testing.T) {
	id := token2.ID{TxId: "tx1", Index: 0}
	var lockCalls int
	base := &mockLocker{lockFunc: func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error {
		lockCalls++

		return nil
	}}
	cache := newLocalLockCache()
	lockerA := &locker{Locker: base, txID: transaction.ID("txA"), localCache: cache, leaseExpiry: time.Minute}
	lockerB := &locker{Locker: base, txID: transaction.ID("txB"), localCache: cache, leaseExpiry: time.Minute}

	require.True(t, lockerA.TryLock(t.Context(), &id))
	require.False(t, lockerB.TryLock(t.Context(), &id), "txB must see txA's fresh lock and not hit the DB")
	assert.Equal(t, 1, lockCalls, "only txA's attempt should reach the underlying locker")
}

// TestLocker_InvalidationAfterUnlockAll verifies that after txA releases its
// tokens, a different transaction can (re)lock them immediately, without
// waiting for leaseExpiry to pass.
func TestLocker_InvalidationAfterUnlockAll(t *testing.T) {
	id := token2.ID{TxId: "tx1", Index: 0}
	var lockCalls int
	base := &mockLocker{lockFunc: func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error {
		lockCalls++

		return nil
	}}
	cache := newLocalLockCache()
	lockerA := &locker{Locker: base, txID: transaction.ID("txA"), localCache: cache, leaseExpiry: time.Minute}
	lockerB := &locker{Locker: base, txID: transaction.ID("txB"), localCache: cache, leaseExpiry: time.Minute}

	require.True(t, lockerA.TryLock(t.Context(), &id))
	require.NoError(t, lockerA.UnlockAll(t.Context()))

	require.True(t, lockerB.TryLock(t.Context(), &id), "txB must be able to lock after txA released")
	assert.Equal(t, 2, lockCalls, "txB's attempt must reach the underlying locker after invalidation")
}

// TestLocker_CachesConflictWithoutRepeatedDBHits verifies that a genuine
// DB-observed lock conflict is also cached, so a repeated attempt on the
// same token doesn't need a second DB round trip to learn the same answer.
func TestLocker_CachesConflictWithoutRepeatedDBHits(t *testing.T) {
	id := token2.ID{TxId: "tx1", Index: 0}
	var lockCalls int
	base := &mockLocker{lockFunc: func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error {
		lockCalls++

		return errors.Wrapf(dbdriver.ErrTokenLockConflict, "token [%v] already locked", tokenID)
	}}
	l := &locker{Locker: base, txID: transaction.ID("txA"), localCache: newLocalLockCache(), leaseExpiry: time.Minute}

	require.False(t, l.TryLock(t.Context(), &id))
	require.False(t, l.TryLock(t.Context(), &id))
	assert.Equal(t, 1, lockCalls, "a genuine conflict must also be cached to avoid a repeated DB hit")
}

// TestLocker_NoCacheAlwaysHitsDB is a regression guard: when localCache is
// nil (cache disabled), every TryLock must still reach the underlying
// locker, preserving pre-cache behavior.
func TestLocker_NoCacheAlwaysHitsDB(t *testing.T) {
	id := token2.ID{TxId: "tx1", Index: 0}
	var lockCalls int
	base := &mockLocker{lockFunc: func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error {
		lockCalls++

		return nil
	}}
	l := &locker{Locker: base, txID: transaction.ID("txA")}

	require.True(t, l.TryLock(t.Context(), &id))
	require.True(t, l.TryLock(t.Context(), &id))
	assert.Equal(t, 2, lockCalls, "with no local cache, every attempt must reach the underlying locker")
}

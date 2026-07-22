/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quietLocker returns a locker whose background scanner effectively never
// wakes up during a test, so tests interact with the locker deterministically.
func quietLocker(t *testing.T, mock *mockTXStatusProvider) *locker {
	t.Helper()
	d := NewLocker(mock, 10*time.Minute, time.Minute).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	return d
}

// TestLockDifferentOwnersDoNotBlock is the core regression test for the
// per-owner sharding: a Lock for owner A that is stuck mid-reclaim (blocked
// inside the status lookup, holding A's lock state) must not prevent a Lock
// for owner B from completing. With the old single global mutex, B blocks
// until A's reclaim finishes.
func TestLockDifferentOwnersDoNotBlock(t *testing.T) {
	mock := newMockTXStatusProvider()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mock.getStatusHook = func(txID string) {
		if txID == "tx-a1" {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	d := quietLocker(t, mock)
	defer close(release)

	tokenA := &token.ID{TxId: "tok-a", Index: 0}
	tokenB := &token.ID{TxId: "tok-b", Index: 0}

	// Owner alice locks tokenA for tx-a1.
	mock.setStatus("tx-a1", ttxdb.Pending)
	_, err := d.Lock(context.Background(), "alice", tokenA, "tx-a1", false)
	require.NoError(t, err)

	// A second transaction tries to reclaim alice's token; the reclaim's
	// status lookup blocks, keeping alice's lock state busy.
	reclaimDone := make(chan struct{})
	go func() {
		defer close(reclaimDone)
		_, _ = d.Lock(context.Background(), "alice", tokenA, "tx-a2", true)
	}()
	<-entered

	// While alice's reclaim is stuck, bob's Lock must still complete.
	bobDone := make(chan error, 1)
	go func() {
		_, err := d.Lock(context.Background(), "bob", tokenB, "tx-b1", false)
		bobDone <- err
	}()

	select {
	case err := <-bobDone:
		require.NoError(t, err, "bob's lock must succeed")
	case <-time.After(2 * time.Second):
		t.Fatal("bob's Lock blocked behind alice's in-flight reclaim: owners are serializing on a shared lock")
	}

	// Cleanup: unblock the reclaim and wait for it to finish.
	release <- struct{}{}
	<-reclaimDone
}

// TestUnlockByTxIDAcrossOwners verifies that unlocking by transaction ID
// still finds and removes that transaction's locks under every owner, and
// leaves other transactions' locks alone.
func TestUnlockByTxIDAcrossOwners(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := quietLocker(t, mock)

	tokenA := &token.ID{TxId: "tok-a", Index: 0}
	tokenB := &token.ID{TxId: "tok-b", Index: 0}
	tokenC := &token.ID{TxId: "tok-c", Index: 0}

	mock.setStatus("tx-1", ttxdb.Pending)
	mock.setStatus("tx-2", ttxdb.Pending)
	_, err := d.Lock(context.Background(), "alice", tokenA, "tx-1", false)
	require.NoError(t, err)
	_, err = d.Lock(context.Background(), "bob", tokenB, "tx-1", false)
	require.NoError(t, err)
	_, err = d.Lock(context.Background(), "alice", tokenC, "tx-2", false)
	require.NoError(t, err)

	d.UnlockByTxID(context.Background(), "tx-1")

	assert.False(t, d.IsLocked(tokenA), "tx-1's token under alice must be unlocked")
	assert.False(t, d.IsLocked(tokenB), "tx-1's token under bob must be unlocked")
	assert.True(t, d.IsLocked(tokenC), "tx-2's token must remain locked")
}

// TestUnlockIDsIsOwnerScoped verifies UnlockIDs removes the given tokens
// from the owner's lock state and reports tokens that were not locked.
func TestUnlockIDsIsOwnerScoped(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := quietLocker(t, mock)

	tokenA := &token.ID{TxId: "tok-a", Index: 0}
	tokenB := &token.ID{TxId: "tok-b", Index: 0}

	mock.setStatus("tx-1", ttxdb.Pending)
	_, err := d.Lock(context.Background(), "alice", tokenA, "tx-1", false)
	require.NoError(t, err)

	notFound := d.UnlockIDs(context.Background(), "alice", tokenA, tokenB)
	require.Len(t, notFound, 1)
	assert.Equal(t, *tokenB, *notFound[0], "tokenB was never locked and must be reported")
	assert.False(t, d.IsLocked(tokenA))
}

// TestEmptyOwnerFallback verifies that callers without owner context (empty
// owner) keep the full lock/relock/reclaim/unlock semantics of the old
// single-map locker.
func TestEmptyOwnerFallback(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := quietLocker(t, mock)

	tokenA := &token.ID{TxId: "tok-a", Index: 0}

	// Lock, then a second lock for another tx must fail.
	mock.setStatus("tx-1", ttxdb.Pending)
	_, err := d.Lock(context.Background(), "", tokenA, "tx-1", false)
	require.NoError(t, err)
	holder, err := d.Lock(context.Background(), "", tokenA, "tx-2", false)
	require.ErrorIs(t, err, AlreadyLockedError)
	assert.Equal(t, "tx-1", holder)
	assert.True(t, d.IsLocked(tokenA))

	// Reclaim succeeds once the holding tx is Deleted.
	mock.setStatus("tx-1", ttxdb.Deleted)
	mock.setStatus("tx-2", ttxdb.Pending)
	_, err = d.Lock(context.Background(), "", tokenA, "tx-2", true)
	require.NoError(t, err)

	// Unlock and lock again.
	notFound := d.UnlockIDs(context.Background(), "", tokenA)
	require.Empty(t, notFound)
	assert.False(t, d.IsLocked(tokenA))
	_, err = d.Lock(context.Background(), "", tokenA, "tx-3", false)
	require.NoError(t, err)
}

// TestSameOwnerStillSerializes verifies sharding did not weaken safety
// within one owner: two transactions racing for the same token under the
// same owner — one wins, the other gets AlreadyLockedError.
func TestSameOwnerStillSerializes(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := quietLocker(t, mock)

	tokenA := &token.ID{TxId: "tok-a", Index: 0}
	mock.setStatus("tx-1", ttxdb.Pending)
	mock.setStatus("tx-2", ttxdb.Pending)

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, txID := range []string{"tx-1", "tx-2"} {
		wg.Go(func() {
			_, err := d.Lock(context.Background(), "alice", tokenA, txID, false)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)

	var failures int
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, AlreadyLockedError)
			failures++
		}
	}
	assert.Equal(t, 1, failures, "exactly one of two racing locks for the same token must fail")
	assert.True(t, d.IsLocked(tokenA))
}

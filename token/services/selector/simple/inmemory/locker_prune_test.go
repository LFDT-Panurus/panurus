/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

func (d *locker) hasShard(owner string) bool {
	d.shardsMu.RLock()
	defer d.shardsMu.RUnlock()
	_, ok := d.shards[owner]

	return ok
}

// TestScannerPrunesEmptyShards verifies that once the scanner has evicted a
// shard's last entry, the shard itself is removed from the registry, while
// shards that still hold locks stay; a pruned owner can lock again afterwards.
func TestScannerPrunesEmptyShards(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := NewLocker(mock, 20*time.Millisecond, time.Minute).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	tokenA := &token.ID{TxId: "tok-a", Index: 0}
	tokenB := &token.ID{TxId: "tok-b", Index: 0}
	mock.setStatus("tx-a", ttxdb.Pending)
	mock.setStatus("tx-b", ttxdb.Pending)
	_, err := d.Lock(context.Background(), "alice", tokenA, "tx-a", false)
	require.NoError(t, err)
	_, err = d.Lock(context.Background(), "bob", tokenB, "tx-b", false)
	require.NoError(t, err)
	require.True(t, d.hasShard("alice"))
	require.True(t, d.hasShard("bob"))

	// tx-a is Deleted: the scanner evicts alice's entry and must then drop
	// alice's now-empty shard, keeping bob's.
	mock.setStatus("tx-a", ttxdb.Deleted)
	require.Eventually(t, func() bool {
		return !d.hasShard("alice")
	}, 2*time.Second, 20*time.Millisecond, "empty shard must be pruned by the scanner")
	require.True(t, d.hasShard("bob"), "shard with live locks must not be pruned")

	// The pruned owner keeps working: a fresh Lock creates a fresh shard.
	mock.setStatus("tx-c", ttxdb.Pending)
	_, err = d.Lock(context.Background(), "alice", tokenA, "tx-c", false)
	require.NoError(t, err)
	require.True(t, d.IsLocked(tokenA))
}

// TestPruningDoesNotLoseConcurrentLocks stresses the race between the
// scanner pruning an empty shard and a concurrent Lock on the same owner:
// a lock taken through a stale shard reference must never end up in an
// orphaned shard invisible to IsLocked/UnlockIDs.
func TestPruningDoesNotLoseConcurrentLocks(t *testing.T) {
	mock := newMockTXStatusProvider()
	// aggressive scan cadence so pruning races with the workers below
	d := NewLocker(mock, time.Millisecond, time.Minute).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	const workers = 4
	const iterations = 500
	var wg sync.WaitGroup
	for w := range workers {
		owner := fmt.Sprintf("owner-%d", w)
		wg.Go(func() {
			runPruningRaceWorker(t, d, mock, owner, iterations)
		})
	}
	wg.Wait()
}

// runPruningRaceWorker repeatedly locks, checks IsLocked, then unlocks a
// fresh token id for owner, failing t if any step observes a lock lost to a
// pruning race.
func runPruningRaceWorker(t *testing.T, d *locker, mock *mockTXStatusProvider, owner string, iterations int) {
	t.Helper()
	ctx := context.Background()
	for i := range iterations {
		id := &token.ID{TxId: fmt.Sprintf("tok-%s-%d", owner, i), Index: 0}
		txID := fmt.Sprintf("tx-%s-%d", owner, i)
		mock.setStatus(txID, ttxdb.Pending)
		if _, err := d.Lock(ctx, owner, id, txID, false); err != nil {
			t.Errorf("lock failed: %v", err)

			return
		}
		// The invariant pruning must not break: a token locked a
		// moment ago is visible as locked. An orphaned shard would
		// make IsLocked return false here.
		if !d.IsLocked(id) {
			t.Errorf("lock for %s lost: shard was pruned while holding a live lock", id)

			return
		}
		if notFound := d.UnlockIDs(ctx, owner, id); len(notFound) != 0 {
			t.Errorf("unlock missed %v: lock ended up in an orphaned shard", notFound)

			return
		}
	}
}

// TestLockSurvivesConcurrentShardPruning pins down the stale-shard-reference
// window deterministically: a Lock that fetched a shard just before the
// scanner pruned it must not write into that unreachable shard, and the public
// entry point must transparently retry on a freshly registered one.
func TestLockSurvivesConcurrentShardPruning(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := quietLocker(t, mock)
	tokenA := &token.ID{TxId: "tok-a", Index: 0}

	// Reproduce what a racing Lock sees: a shard reference obtained from the
	// registry that has been pruned out of it in the meantime.
	stale := d.getOrCreateShard("alice")
	stale.mu.Lock()
	d.pruneEmptyShard("alice", stale)
	stale.mu.Unlock()
	require.True(t, stale.pruned, "pruning must mark the shard")
	require.False(t, d.hasShard("alice"))

	// Locking through the stale reference must be refused, not silently lost.
	mock.setStatus("tx-1", ttxdb.Pending)
	_, err := d.lockInShard(context.Background(), stale, "alice", tokenA, "tx-1", false)
	require.ErrorIs(t, err, errShardPruned)
	require.Empty(t, stale.locked, "no entry may be written to a pruned shard")

	// Lock retries on a fresh shard, so the lock is visible to everyone.
	_, err = d.Lock(context.Background(), "alice", tokenA, "tx-1", false)
	require.NoError(t, err)
	require.True(t, d.hasShard("alice"))
	require.True(t, d.IsLocked(tokenA))
	require.Empty(t, stale.locked, "the pruned shard must stay empty")
	require.Empty(t, d.UnlockIDs(context.Background(), "alice", tokenA))
}

// TestPruningEmptyShardKeepsNewerShard verifies that pruning a stale empty
// shard does not evict a newer shard registered for the same owner, which
// would orphan that shard's live locks.
func TestPruningEmptyShardKeepsNewerShard(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := quietLocker(t, mock)
	tokenA := &token.ID{TxId: "tok-a", Index: 0}

	stale := d.getOrCreateShard("alice")
	stale.mu.Lock()
	d.pruneEmptyShard("alice", stale)
	stale.mu.Unlock()

	mock.setStatus("tx-1", ttxdb.Pending)
	_, err := d.Lock(context.Background(), "alice", tokenA, "tx-1", false)
	require.NoError(t, err)

	// A late prune of the stale shard must leave alice's live shard registered.
	stale.mu.Lock()
	d.pruneEmptyShard("alice", stale)
	stale.mu.Unlock()

	require.True(t, d.hasShard("alice"), "the live shard must stay registered")
	require.True(t, d.IsLocked(tokenA))
}

// TestLockedCountDoesNotDeadlockWithPruning is the regression test for the
// lock-order inversion between the registry mutex and a shard mutex: the
// scanner's lockedCount used to hold shardsMu while taking a shard lock, while
// pruneEmptyShard takes shardsMu with a shard lock already held. Under load
// the two orders deadlock and every locker operation stalls forever.
func TestLockedCountDoesNotDeadlockWithPruning(t *testing.T) {
	mock := newMockTXStatusProvider()
	// aggressive cadence so the scanner's lockedCount overlaps the prunes
	// triggered by the workers' unlocks
	d := NewLocker(mock, time.Millisecond, time.Minute).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	const workers = 4
	const iterations = 300
	// Workers report through a channel rather than t: the test may return on
	// the timeout below while they are still running.
	errCh := make(chan error, workers)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for w := range workers {
			owner := fmt.Sprintf("owner-%d", w)
			wg.Go(func() {
				runDeadlockRaceWorker(d, mock, errCh, owner, iterations)
			})
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("lockedCount and shard pruning deadlocked: registry and shard mutexes are taken in inconsistent order")
	}
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	require.Zero(t, d.lockedCount())
}

// runDeadlockRaceWorker repeatedly locks, races the scanner's lockedCount,
// then unlocks a fresh token id for owner, reporting any failure on errCh
// rather than failing t directly (the test may already have returned on
// timeout by the time a worker finishes).
func runDeadlockRaceWorker(d *locker, mock *mockTXStatusProvider, errCh chan error, owner string, iterations int) {
	ctx := context.Background()
	for i := range iterations {
		id := &token.ID{TxId: fmt.Sprintf("tok-%s-%d", owner, i), Index: 0}
		txID := fmt.Sprintf("tx-%s-%d", owner, i)
		mock.setStatus(txID, ttxdb.Pending)
		if _, err := d.Lock(ctx, owner, id, txID, false); err != nil {
			errCh <- errors.Wrapf(err, "lock of [%s] failed", id)

			return
		}
		// racing the scanner's own lockedCount from the caller side
		_ = d.lockedCount()
		if notFound := d.UnlockIDs(ctx, owner, id); len(notFound) != 0 {
			errCh <- errors.Errorf("unlock missed %v", notFound)

			return
		}
	}
}

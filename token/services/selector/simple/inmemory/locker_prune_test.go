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
		})
	}
	wg.Wait()
}

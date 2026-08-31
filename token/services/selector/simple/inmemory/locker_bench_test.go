/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// BenchmarkLockerContention measures lock/unlock throughput under concurrent
// load. Workers are spread over a varying number of owners: owners=1 puts
// every worker on one shard — approximating the old single global mutex —
// while owners=workers gives every worker its own shard.
func BenchmarkLockerContention(b *testing.B) {
	const workers = 32
	for _, owners := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("workers=%d/owners=%d", workers, owners), func(b *testing.B) {
			mock := newMockTXStatusProvider()
			d := NewLocker(mock, 10*time.Minute, time.Minute).(*locker)
			b.Cleanup(func() { _ = d.Stop() })

			var next atomic.Int64
			b.ResetTimer()
			var wg sync.WaitGroup
			for w := range workers {
				owner := fmt.Sprintf("owner-%d", w%owners)
				wg.Go(func() {
					runContentionWorker(b, d, owner, &next, int64(b.N))
				})
			}
			wg.Wait()
		})
	}
}

// runContentionWorker repeatedly claims the next work index via next and
// performs one lock/unlock cycle for it, until the index reaches n.
func runContentionWorker(b *testing.B, d *locker, owner string, next *atomic.Int64, n int64) {
	b.Helper()
	ctx := context.Background()
	for {
		i := next.Add(1) - 1
		if i >= n {
			return
		}
		id := &token.ID{TxId: fmt.Sprintf("tok-%s-%d", owner, i), Index: 0}
		txID := fmt.Sprintf("tx-%d", i)
		if _, err := d.Lock(ctx, owner, id, txID, false); err != nil {
			b.Error(err)

			return
		}
		d.UnlockIDs(ctx, owner, id)
	}
}

// BenchmarkLockerContentionWithSlowReclaim is the scenario the sharding
// targets: one owner is stuck in a reclaim whose status lookup is slow
// (e.g. a database round trip), while other owners keep locking. With one
// shared lock, everybody waits behind the slow reclaim; with per-owner
// shards, only the slow owner does.
func BenchmarkLockerContentionWithSlowReclaim(b *testing.B) {
	const workers = 8
	const statusDelay = 100 * time.Microsecond
	for _, owners := range []int{1, workers} {
		b.Run(fmt.Sprintf("workers=%d/owners=%d", workers, owners), func(b *testing.B) {
			mock := newMockTXStatusProvider()
			mock.setGetStatusHook(func(string) { time.Sleep(statusDelay) })
			d := NewLocker(mock, 10*time.Minute, time.Minute).(*locker)
			b.Cleanup(func() { _ = d.Stop() })

			// Every worker's tokens are pre-locked by a Pending tx, so each
			// Lock attempt takes the reclaim path and pays the status lookup.
			mock.setStatus("tx-holder", ttxdb.Pending)
			ids := make([]*token.ID, workers)
			ownersOf := make([]string, workers)
			for w := range workers {
				ownersOf[w] = fmt.Sprintf("owner-%d", w%owners)
				ids[w] = &token.ID{TxId: fmt.Sprintf("tok-%d", w), Index: 0}
				if _, err := d.Lock(context.Background(), ownersOf[w], ids[w], "tx-holder", false); err != nil {
					b.Fatal(err)
				}
			}

			var next atomic.Int64
			b.ResetTimer()
			var wg sync.WaitGroup
			for w := range workers {
				wg.Go(func() {
					runSlowReclaimWorker(d, ownersOf[w], ids[w], &next, int64(b.N))
				})
			}
			wg.Wait()
		})
	}
}

// runSlowReclaimWorker repeatedly claims the next work index via next and
// attempts a reclaiming Lock on id (which always fails, since its holder tx
// stays Pending), until the index reaches n.
func runSlowReclaimWorker(d *locker, owner string, id *token.ID, next *atomic.Int64, n int64) {
	ctx := context.Background()
	for {
		i := next.Add(1) - 1
		if i >= n {
			return
		}
		// reclaim fails (holder is Pending) after paying the
		// status lookup under the shard lock
		_, _ = d.Lock(ctx, owner, id, fmt.Sprintf("tx-%d", i), true)
	}
}

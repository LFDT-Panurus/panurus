/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"context"
	"sync"
	"time"

	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// statusBatchWindow is how long a statusBatcher waits after its first
// pending lookup before flushing, to give concurrent callers a chance to
// join the same batched query. Every call pays this as a fixed latency tax
// even when nothing joins it, so it's kept small relative to a typical DB
// round trip rather than tuned for maximum coalescing.
const statusBatchWindow = 300 * time.Microsecond

// statusFetcher resolves the status of many transaction ids in one round trip.
type statusFetcher interface {
	GetStatuses(ctx context.Context, txIDs []string) (map[string]dbdriver.TxStatus, error)
}

// statusBatcher coalesces concurrent single-tx status lookups that land
// within statusBatchWindow into a single batched GetStatuses call.
type statusBatcher struct {
	fetch  statusFetcher
	window time.Duration

	mu      sync.Mutex
	pending *statusBatch
}

type statusBatch struct {
	ids     []string
	waiters map[string][]chan statusResult
}

type statusResult struct {
	status dbdriver.TxStatus
	err    error
}

func newStatusBatcher(fetch statusFetcher) *statusBatcher {
	return &statusBatcher{fetch: fetch, window: statusBatchWindow}
}

// Get returns the status of txID, coalescing this call with any others that
// arrive within the batch window into a single GetStatuses query. If ctx is
// cancelled while waiting, Get returns ctx.Err(); the batched query still
// completes on behalf of the other waiters.
func (b *statusBatcher) Get(ctx context.Context, txID string) (dbdriver.TxStatus, error) {
	ch := make(chan statusResult, 1)

	b.mu.Lock()
	if b.pending == nil {
		b.pending = &statusBatch{waiters: map[string][]chan statusResult{}}
		batch := b.pending
		time.AfterFunc(b.window, func() { b.flush(batch) })
	}
	batch := b.pending
	batch.ids = append(batch.ids, txID)
	batch.waiters[txID] = append(batch.waiters[txID], ch)
	b.mu.Unlock()

	select {
	case res := <-ch:
		return res.status, res.err
	case <-ctx.Done():
		return dbdriver.Unknown, ctx.Err()
	}
}

// flush resolves a batch's transaction ids in one call and hands each
// waiter its result. It detaches the batch from b.pending first so that
// callers arriving after this point start a new batch instead of joining
// one that's already being flushed. The lookup deliberately runs on
// context.Background(): the batch serves multiple callers, so it must not
// be tied to any single caller's context.
func (b *statusBatcher) flush(batch *statusBatch) {
	b.mu.Lock()
	if b.pending == batch {
		b.pending = nil
	}
	b.mu.Unlock()

	statuses, err := b.fetch.GetStatuses(context.Background(), batch.ids)
	for txID, waiters := range batch.waiters {
		res := statusResult{err: err}
		if err == nil {
			// zero value is dbdriver.Unknown, matching GetStatus's not-found semantics
			res.status = statuses[txID]
		}
		for _, ch := range waiters {
			ch <- res
		}
	}
}

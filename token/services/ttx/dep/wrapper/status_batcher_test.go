/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"context"
	"errors"
	"sync"
	"testing"

	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStatusFetcher struct {
	mu        sync.Mutex
	calls     [][]string
	responses map[string]dbdriver.TxStatus
	err       error
	block     chan struct{}
}

func (f *fakeStatusFetcher) GetStatuses(_ context.Context, txIDs []string) (map[string]dbdriver.TxStatus, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), txIDs...))
	block := f.block
	f.mu.Unlock()

	if block != nil {
		<-block
	}

	if f.err != nil {
		return nil, f.err
	}

	result := make(map[string]dbdriver.TxStatus, len(txIDs))
	for _, id := range txIDs {
		if s, ok := f.responses[id]; ok {
			result[id] = s
		}
	}

	return result, nil
}

func (f *fakeStatusFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func TestStatusBatcher_SingleLookup(t *testing.T) {
	fetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{"tx1": dbdriver.Confirmed}}
	b := newStatusBatcher(fetch)

	status, err := b.Get(t.Context(), "tx1")
	require.NoError(t, err)
	assert.Equal(t, dbdriver.Confirmed, status)
	assert.Equal(t, 1, fetch.callCount())
}

func TestStatusBatcher_MissingTxIsUnknown(t *testing.T) {
	fetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{}}
	b := newStatusBatcher(fetch)

	status, err := b.Get(t.Context(), "missing")
	require.NoError(t, err)
	assert.Equal(t, dbdriver.Unknown, status)
}

func TestStatusBatcher_CoalescesConcurrentLookups(t *testing.T) {
	fetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{
		"tx1": dbdriver.Confirmed,
		"tx2": dbdriver.Pending,
		"tx3": dbdriver.Deleted,
	}}
	b := newStatusBatcher(fetch)

	ids := []string{"tx1", "tx2", "tx3"}
	results := make([]dbdriver.TxStatus, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			s, err := b.Get(t.Context(), id)
			assert.NoError(t, err)
			results[i] = s
		}(i, id)
	}
	wg.Wait()

	assert.Equal(t, []dbdriver.TxStatus{dbdriver.Confirmed, dbdriver.Pending, dbdriver.Deleted}, results)
	assert.Equal(t, 1, fetch.callCount(), "concurrent lookups should be coalesced into a single batch")
}

func TestStatusBatcher_DuplicateTxIDInSameBatch(t *testing.T) {
	fetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{"tx1": dbdriver.Confirmed}}
	b := newStatusBatcher(fetch)

	results := make([]dbdriver.TxStatus, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := b.Get(t.Context(), "tx1")
			assert.NoError(t, err)
			results[i] = s
		}(i)
	}
	wg.Wait()

	assert.Equal(t, []dbdriver.TxStatus{dbdriver.Confirmed, dbdriver.Confirmed}, results)
	require.Equal(t, 1, fetch.callCount())
	assert.Equal(t, []string{"tx1", "tx1"}, fetch.calls[0], "both waiters for the same tx id should be included in the batch request")
}

func TestStatusBatcher_SeparateBatchesAfterFlush(t *testing.T) {
	fetch := &fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{"tx1": dbdriver.Confirmed, "tx2": dbdriver.Confirmed}}
	b := newStatusBatcher(fetch)

	// Get blocks until its own batch flushes, so these two calls can never
	// land in the same window.
	_, err := b.Get(t.Context(), "tx1")
	require.NoError(t, err)
	_, err = b.Get(t.Context(), "tx2")
	require.NoError(t, err)

	assert.Equal(t, 2, fetch.callCount(), "lookups that don't overlap in time should not be coalesced")
}

func TestStatusBatcher_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	fetch := &fakeStatusFetcher{err: wantErr}
	b := newStatusBatcher(fetch)

	_, err := b.Get(t.Context(), "tx1")
	require.ErrorIs(t, err, wantErr)
}

func TestStatusBatcher_HonorsContextCancellation(t *testing.T) {
	block := make(chan struct{})
	fetch := &fakeStatusFetcher{
		responses: map[string]dbdriver.TxStatus{"tx1": dbdriver.Confirmed},
		block:     block,
	}
	b := newStatusBatcher(fetch)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := b.Get(ctx, "tx1")
		done <- err
	}()

	cancel()
	err := <-done
	require.ErrorIs(t, err, context.Canceled)

	// unblock the in-flight fetch so its goroutine can finish
	close(block)
}

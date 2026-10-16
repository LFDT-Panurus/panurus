/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// gatedSpendableIterator blocks the first Next until release is closed, signalling on
// started once it is parked there. That models the window the cache-invalidation race
// lives in: the snapshot has been read from the store, but update has not installed it yet.
type gatedSpendableIterator struct {
	tokens  []*token2.UnspentTokenInWallet
	i       int
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (it *gatedSpendableIterator) Next() (*token2.UnspentTokenInWallet, error) {
	it.once.Do(func() {
		close(it.started)
		<-it.release
	})
	if it.i >= len(it.tokens) {
		return nil, nil
	}
	t := it.tokens[it.i]
	it.i++

	return t, nil
}

func (it *gatedSpendableIterator) Close() {}

// gatedTokenDB hands out a gated iterator for the first read and plain ones afterwards, so
// a test can park exactly one update inside the store read.
type gatedTokenDB struct {
	mu      sync.Mutex
	calls   int
	tokens  []*token2.UnspentTokenInWallet
	started chan struct{}
	release chan struct{}
}

func (db *gatedTokenDB) SpendableTokensIteratorBy(context.Context, string, token2.Type) (driver.SpendableTokensIterator, error) {
	db.mu.Lock()
	db.calls++
	first := db.calls == 1
	db.mu.Unlock()
	if first {
		return &gatedSpendableIterator{tokens: db.tokens, started: db.started, release: db.release}, nil
	}

	return &gatedSpendableIterator{tokens: db.tokens, started: make(chan struct{}), release: closedChan()}, nil
}

func (db *gatedTokenDB) HasEnoughSpendableTokens(context.Context, string, token2.Type, *big.Int) (bool, error) {
	return true, nil
}

func (db *gatedTokenDB) callCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.calls
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)

	return c
}

// TestCachedFetcher_DiscardsSnapshotInvalidatedMidRead pins the cache-invalidation TOCTOU:
// an update whose store read began before InvalidateCache must not install that snapshot
// and must not stamp it fresh, because the snapshot cannot contain whatever made the caller
// invalidate. Before the fix, update re-checked only staleness - which an invalidation
// satisfies - so it installed the pre-invalidation snapshot and marked it fresh, silently
// losing the refresh for a whole freshness interval.
func TestCachedFetcher_DiscardsSnapshotInvalidatedMidRead(t *testing.T) {
	db := &gatedTokenDB{
		tokens: []*token2.UnspentTokenInWallet{
			{WalletID: "wallet1", Type: "USD", Quantity: "100"},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	// A long freshness interval so that only an invalidation, never the clock, can make the
	// installed snapshot stale.
	f := NewCachedFetcher(db, 0, time.Hour, 100)

	done := make(chan struct{})
	go func() {
		defer close(done)
		f.update(context.Background())
	}()

	select {
	case <-db.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the update never reached the store read")
	}

	// The selector found a stale candidate while the snapshot above was in flight.
	f.InvalidateCache()
	close(db.release)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the update never returned")
	}

	require.True(t, f.isCacheStale(),
		"a snapshot read before the invalidation was installed and stamped fresh, so the "+
			"invalidation is lost until the freshness interval elapses")

	// And the next read actually refetches, rather than being served the discarded snapshot.
	_, err := f.UnspentTokensIteratorBy(context.Background(), "wallet1", "USD")
	require.NoError(t, err)
	require.Equal(t, 2, db.callCount(), "the invalidated cache was not refreshed on the next read")
	require.False(t, f.isCacheStale(), "the post-invalidation snapshot should be fresh")
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelectorClosesDisplacedIterators verifies that every iterator the retry
// loop replaces is closed. Previously `selectInternal` overwrote `s.cache` on
// each immediate retry without closing the iterator it displaced, leaking a
// database cursor and its pooled connection per retry. See #2019.
func TestSelectorClosesDisplacedIterators(t *testing.T) {
	_, metrics := setupMetricsMocks()

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeTokenLocker{}

	var mu sync.Mutex
	var iterators []*mocks.FakeIterator[*token2.UnspentTokenInWallet]
	mockFetcher.UnspentTokensIteratorByStub = func(_ context.Context, _ string, _ token2.Type, _ int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		// One token, always locked by someone else, then exhausted: this drives a
		// refresh of the cache on every pass through the loop.
		it.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: "100",
		}, nil)
		it.NextReturnsOnCall(1, nil, nil)

		mu.Lock()
		defer mu.Unlock()
		iterators = append(iterators, it)

		return it, nil
	}
	// Every token appears locked by another process, so the selector exhausts its
	// immediate retries and gives up with SufficientButLockedFunds.
	mockLocker.TryLockReturns(false, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, iterators, "the retry loop must have refreshed the cache at least once")
	// All but the last iterator were displaced by a refresh and must be closed.
	for i, it := range iterators[:len(iterators)-1] {
		assert.Equal(t, 1, it.CloseCallCount(), "displaced iterator %d was leaked", i)
	}
	// The last one is still the current cache; Close must release it.
	require.NoError(t, s.Close())
	assert.Equal(t, 1, iterators[len(iterators)-1].CloseCallCount(), "the current iterator must be closed by Close")
}

// TestSelectorCloseDuringRetryIsRaceFree runs Close concurrently with a retrying
// Select. Under `-race` this covers the unsynchronized reads and writes of
// `s.cache` inside `selectInternal`, which could also nil-dereference when Close
// won the race between the closed check and the iterator read. See #2019.
func TestSelectorCloseDuringRetryIsRaceFree(t *testing.T) {
	_, metrics := setupMetricsMocks()

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeTokenLocker{}

	var created atomic.Int64
	mockFetcher.UnspentTokensIteratorByStub = func(_ context.Context, _ string, _ token2.Type, _ int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		created.Add(1)
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		it.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: "100",
		}, nil)
		it.NextReturnsOnCall(1, nil, nil)

		return it, nil
	}
	mockLocker.TryLockReturns(false, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Either outcome is valid: locked funds, or "already closed" if Close won.
		_, _, _ = s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	}()
	go func() {
		defer wg.Done()
		// Give the selector a chance to enter the retry loop before closing it.
		for created.Load() == 0 {
			runtime.Gosched()
		}
		_ = s.Close()
	}()
	wg.Wait()

	// Whoever ran last, the selector ends up closed and stays closed.
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.ErrorContains(t, err, "selector is already closed")
}

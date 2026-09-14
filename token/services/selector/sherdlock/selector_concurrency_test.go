/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closeTrackingFetcher returns a fetcher whose iterators record Close, and whose
// Next panics if called after Close — the in-process stand-in for reading from a
// closed sql.Rows.
func closeTrackingFetcher() *mocks.FakeTokenFetcher {
	mockFetcher := &mocks.FakeTokenFetcher{}
	mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type, int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		var mu sync.Mutex
		closed := false
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		it.CloseStub = func() {
			mu.Lock()
			defer mu.Unlock()
			closed = true
		}
		it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
			mu.Lock()
			defer mu.Unlock()
			if closed {
				panic("Next called on a closed iterator")
			}

			return &token2.UnspentTokenInWallet{
				Id:       token2.ID{TxId: "tx1", Index: 0},
				Type:     "ABC",
				Quantity: "1",
			}, nil
		}

		return it, nil
	}

	return mockFetcher
}

// TestStubbornSelectorCloseDrainsInFlightSelect covers the drain that keeps
// Close() from closing the token iterator while a selection is reading it.
//
// StubbornSelector overrides Select, so guarding only Selector.Select leaves the
// default production path unprotected: NewSherdSelector returns a
// StubbornSelector for any backoff >= 0, and retryInterval defaults to 5s. Run
// with -race to see the unguarded version fail.
func TestStubbornSelectorCloseDrainsInFlightSelect(t *testing.T) {
	_, metrics := setupMetricsMocks()

	for _, tc := range []struct {
		name string
		new  func(fetcher sherdlock.TokenFetcher, locker sherdlock.TokenLocker) interface {
			Select(context.Context, token.OwnerFilter, string, token2.Type) ([]*token2.ID, token2.Quantity, error)
			Close() error
		}
	}{
		{
			name: "Selector",
			new: func(fetcher sherdlock.TokenFetcher, locker sherdlock.TokenLocker) interface {
				Select(context.Context, token.OwnerFilter, string, token2.Type) ([]*token2.ID, token2.Quantity, error)
				Close() error
			} {
				return sherdlock.NewSelector(sherdlock.Logger(), fetcher, locker, 64, 100000, 100000, time.Minute, metrics)
			},
		},
		{
			name: "StubbornSelector",
			new: func(fetcher sherdlock.TokenFetcher, locker sherdlock.TokenLocker) interface {
				Select(context.Context, token.OwnerFilter, string, token2.Type) ([]*token2.ID, token2.Quantity, error)
				Close() error
			} {
				return sherdlock.NewStubbornSelector(sherdlock.Logger(), fetcher, locker, 64, time.Millisecond, 100, 100000, 100000, time.Minute, metrics)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockLocker := &mocks.FakeTokenLocker{}
			mockLocker.TryLockReturns(false, nil) // never satisfied: keep iterating
			s := tc.new(closeTrackingFetcher(), mockLocker)

			var wg sync.WaitGroup
			wg.Go(func() {
				// An unsatisfiable request keeps the selection looping over the
				// iterator while Close() runs.
				_, _, _ = s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "9999999", "ABC")
			})

			// Give the selection a moment to get into the iteration loop.
			time.Sleep(20 * time.Millisecond)
			require.NoError(t, s.Close())
			wg.Wait()

			// Once closed, further selections must be refused rather than
			// touching the released iterator.
			_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "1", "ABC")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "already closed")
		})
	}
}

// TestSelectorZeroSelectionTimeoutMeansNoTimeout guards the zero value of
// selectionTimeout for both selector flavours. context.WithTimeout(ctx, 0)
// returns an already-expired context, so an omitted timeout would make every
// Select fail with SelectorTimedOut after examining zero tokens. Manager takes
// its limits from a Config struct, which makes the field easy to leave unset.
func TestSelectorZeroSelectionTimeoutMeansNoTimeout(t *testing.T) {
	_, metrics := setupMetricsMocks()

	fetcher := func() *mocks.FakeTokenFetcher {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type, int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			remaining := 1
			it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
			it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
				if remaining == 0 {
					return nil, nil
				}
				remaining--

				return &token2.UnspentTokenInWallet{
					Id:       token2.ID{TxId: "tx1", Index: 0},
					Type:     "ABC",
					Quantity: "10",
				}, nil
			}

			return it, nil
		}

		return mockFetcher
	}

	for _, timeout := range []time.Duration{0, -time.Second} {
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), fetcher(), mockLocker, 64, 100000, 100000, timeout, metrics)
		ids, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "10", "ABC")
		require.NoError(t, err, "selectionTimeout %v must mean no timeout", timeout)
		assert.Len(t, ids, 1)
		assert.Equal(t, "10", sum.Decimal())

		stubborn := sherdlock.NewStubbornSelector(sherdlock.Logger(), fetcher(), mockLocker, 64,
			time.Millisecond, 3, 100000, 100000, timeout, metrics)
		ids, sum, err = stubborn.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "10", "ABC")
		require.NoError(t, err, "selectionTimeout %v must mean no timeout for StubbornSelector", timeout)
		assert.Len(t, ids, 1)
		assert.Equal(t, "10", sum.Decimal())
	}
}

// TestStubbornSelectorReleasesLocksBeforeBackoff covers the livelock guard: a
// cycle that ends in "sufficient funds but locked" must release the locks it
// took before sleeping, otherwise two selections each sitting on part of the
// funds both spin out their retry budget and both report insufficient funds
// while the funds were available the whole time.
func TestStubbornSelectorReleasesLocksBeforeBackoff(t *testing.T) {
	_, metrics := setupMetricsMocks()

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type, int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		// Two tokens per cycle, then exhaustion, so each cycle can lock one and
		// find the other taken — the "sufficient but locked" outcome.
		remaining := uint64(2)
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
			if remaining == 0 {
				return nil, nil
			}
			remaining--

			return &token2.UnspentTokenInWallet{
				Id:       token2.ID{TxId: "tx1", Index: remaining},
				Type:     "ABC",
				Quantity: "10",
			}, nil
		}

		return it, nil
	}

	mockLocker := &mocks.FakeTokenLocker{}
	// Every token appears to be held by another process, so each cycle ends in
	// "sufficient funds but locked" and the selector backs off.
	mockLocker.TryLockReturns(false, nil)

	const retriesAfterBackoff = 3
	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64,
		time.Millisecond, retriesAfterBackoff, 100000, 100000, time.Minute, metrics)

	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "15", "ABC")
	require.Error(t, err)
	assert.False(t, errors.Is(err, token.SelectorTimedOut), "expected the retry budget to bind, not the timeout")

	// One release per backoff, plus the final one on the terminal error. Without
	// the pre-backoff release this is 1.
	assert.GreaterOrEqual(t, mockLocker.UnlockAllCallCount(), retriesAfterBackoff,
		"locks must be released before each backoff so a competing selection can proceed")
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
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

func TestSelectorUnit(t *testing.T) {
	_, metrics := setupMetricsMocks()

	t.Run("SelectSuccess", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)

		mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		mockIt.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: "100",
		}, nil)
		mockIt.NextReturnsOnCall(1, nil, nil)

		mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)
		mockLocker.TryLockReturns(true, nil)

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.NoError(t, err)
		assert.Len(t, tokens, 1)
		assert.Equal(t, "100", sum.Decimal())
	})

	t.Run("InsufficientFunds", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)

		mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		mockIt.NextReturns(nil, nil)
		mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient funds")
	})

	t.Run("ClosedError", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s2 := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 2, 10000, 50000, 30*time.Second, metrics)
		err := s2.Close()
		require.NoError(t, err)

		_, _, err = s2.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "selector is already closed")
	})

	t.Run("FetcherError", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)

		mockFetcher.UnspentTokensIteratorByReturns(nil, errors.New("fetcher error"))
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetcher error")
	})
}

func TestStubbornSelectorUnit(t *testing.T) {
	_, metrics := setupMetricsMocks()

	t.Run("SelectSuccessAfterImmediateRetries", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 100*time.Millisecond, 2, 10000, 50000, 30*time.Second, metrics)

		mockFetcher.UnspentTokensIteratorByStub = func(ctx context.Context, walletID string, tokenType token2.Type, limit int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
			mockIt.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
				Id:       token2.ID{TxId: "tx1", Index: 0},
				Type:     "ABC",
				Quantity: "100",
			}, nil)
			mockIt.NextReturnsOnCall(1, nil, nil)

			return mockIt, nil
		}

		// Fails first lock attempt, succeeds on second
		mockLocker.TryLockReturnsOnCall(0, false, nil)
		mockLocker.TryLockReturnsOnCall(1, true, nil)

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.NoError(t, err)
		assert.Len(t, tokens, 1)
		assert.Equal(t, "100", sum.Decimal())
	})

	t.Run("ContextCanceled", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 100*time.Millisecond, 2, 10000, 50000, 30*time.Second, metrics)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		mockIt.NextReturns(nil, nil)
		mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

		_, _, err := s.Select(ctx, &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
	})

	t.Run("MaxRetriesExceeded", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}

		mockFetcher.UnspentTokensIteratorByStub = func(ctx context.Context, walletID string, tokenType token2.Type, limit int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
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

		shortBackoffS := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 1*time.Millisecond, 1, 10000, 50000, 30*time.Second, metrics)
		_, _, err := shortBackoffS.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
	})
}

// TestSelectorRateLimit verifies that when the locker denies a lock with an error
// wrapping token.SelectorRateLimited, the selector aborts immediately and surfaces
// that error instead of retrying. This is the contract an application-supplied,
// wallet-id-aware Locker uses to integrate its own rate limiting.
func TestSelectorRateLimit(t *testing.T) {
	_, metrics := setupMetricsMocks()

	t.Run("AbortsOnRateLimitedLock", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}

		mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		mockIt.NextReturns(&token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: "100",
		}, nil)
		mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

		// The locker denies the lock with a rate-limit error.
		mockLocker.TryLockReturns(false, errors.Wrapf(token.SelectorRateLimited, "wallet alice throttled"))

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
		assert.True(t, errors.Is(err, token.SelectorRateLimited), "expected rate-limit error, got: %v", err)
		// Fail-fast: the selector must not spin retrying on a rate-limited lock.
		assert.Equal(t, 1, mockLocker.TryLockCallCount(), "selector must abort after the first rate-limited lock")
	})

	// ReleasesLocksOnRateLimitedAbort verifies that when the selector has already
	// locked one or more tokens and a subsequent lock is denied with
	// token.SelectorRateLimited, it releases everything via UnlockAll before
	// returning. The abort path must not leak the tokens locked so far.
	t.Run("ReleasesLocksOnRateLimitedAbort", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}

		// Three tokens of 30 each. The request for 70 forces the selector to lock
		// the first two (60, still short) before it reaches the third.
		mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		mockIt.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: "30",
		}, nil)
		mockIt.NextReturnsOnCall(1, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx2", Index: 0},
			Type:     "ABC",
			Quantity: "30",
		}, nil)
		mockIt.NextReturnsOnCall(2, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx3", Index: 0},
			Type:     "ABC",
			Quantity: "30",
		}, nil)
		mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)

		// Lock the first two tokens successfully, then hit the rate limit on the third.
		mockLocker.TryLockReturnsOnCall(0, true, nil)
		mockLocker.TryLockReturnsOnCall(1, true, nil)
		mockLocker.TryLockReturnsOnCall(2, false, errors.Wrapf(token.SelectorRateLimited, "wallet alice throttled"))

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 10000, 50000, 30*time.Second, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "70", "ABC")
		require.Error(t, err)
		assert.True(t, errors.Is(err, token.SelectorRateLimited), "expected rate-limit error, got: %v", err)
		// Two tokens were locked before the rate-limited third lock aborted selection.
		assert.Equal(t, 3, mockLocker.TryLockCallCount())
		// The abort path must release the already-locked tokens.
		assert.Equal(t, 1, mockLocker.UnlockAllCallCount(), "selector must release locked tokens via UnlockAll on abort")
	})
}

// TestSelectorTimeout verifies that when the configured selection timeout fires,
// both Selector and StubbornSelector return an error wrapping token.SelectorTimedOut
// (not SelectorSufficientButLockedFunds, which would incorrectly invite retries).
//
// The test sets up an iterator stub that is returned by UnspentTokensIteratorBy,
// which receives the internal timeoutCtx as its first argument. The iterator's
// Next method checks that context so the selector loop exits with
// context.DeadlineExceeded once the timeout fires — exactly the error path that
// must produce SelectorTimedOut.
func TestSelectorTimeout(t *testing.T) {
	_, metrics := setupMetricsMocks()

	// makeFetcher returns a TokenFetcher whose UnspentTokensIteratorBy produces an
	// iterator that respects the context it was called with (which is timeoutCtx
	// inside selectInternal).  Next returns a token while the context is live; once
	// the deadline fires it returns (nil, ctx.Err()) so selectInternal propagates
	// context.DeadlineExceeded to Select / StubbornSelector.Select.
	makeFetcher := func() *mocks.FakeTokenFetcher {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockFetcher.UnspentTokensIteratorByStub = func(ctx context.Context, _ string, _ token2.Type, _ int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
			it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
				if ctx.Err() != nil {
					return nil, ctx.Err()
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

	t.Run("SelectorTimesOut", func(t *testing.T) {
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(false, nil) // all tokens appear locked by others

		s := sherdlock.NewSelector(sherdlock.Logger(), makeFetcher(), mockLocker, 64, 100000, 100000, 20*time.Millisecond, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "9999999", "ABC")
		require.Error(t, err)
		assert.True(t, errors.Is(err, token.SelectorTimedOut), "expected SelectorTimedOut, got: %v", err)
		assert.False(t, errors.Is(err, token.SelectorSufficientButLockedFunds), "timeout must not be reported as SelectorSufficientButLockedFunds")
	})

	t.Run("StubbornSelectorTimesOut", func(t *testing.T) {
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(false, nil)

		s := sherdlock.NewStubbornSelector(sherdlock.Logger(), makeFetcher(), mockLocker, 64, 1*time.Millisecond, 100, 100000, 100000, 20*time.Millisecond, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "9999999", "ABC")
		require.Error(t, err)
		assert.True(t, errors.Is(err, token.SelectorTimedOut), "expected SelectorTimedOut, got: %v", err)
		assert.False(t, errors.Is(err, token.SelectorSufficientButLockedFunds), "timeout must not be reported as SelectorSufficientButLockedFunds")
	})
}

// TestSelectorResourceLimits verifies the two resource-limit abort branches in
// selectInternal actually fire under sherdlock. Unlike the simple driver, which
// walks a single iterator, sherdlock refreshes its cache on every immediate
// retry (fetch a fresh iterator, swapCache, keep iterating). The tokensIterated
// and lockAttempts counters must therefore accumulate across those cache swaps,
// not reset with each reloaded iterator. Both cases below deliberately drive the
// selection through multiple reloads before the limit is hit, so they exercise
// exactly the cache-swap-on-retry path that the simple driver's tests do not.
func TestSelectorResourceLimits(t *testing.T) {
	_, metrics := setupMetricsMocks()

	// makeBatchFetcher returns a TokenFetcher whose UnspentTokensIteratorBy hands
	// back a fresh iterator of batchSize tokens on every call, with globally
	// unique IDs across batches. Each iterator ends in (nil, nil), which — while
	// tokens still appear locked by others — sends selectInternal back through the
	// reload/swapCache path for another batch.
	makeBatchFetcher := func(batchSize int) *mocks.FakeTokenFetcher {
		f := &mocks.FakeTokenFetcher{}
		var idx uint64 // running token index across all batches
		f.UnspentTokensIteratorByStub = func(_ context.Context, _ string, _ token2.Type, _ int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
			var n int // tokens emitted from this batch
			it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
				if n >= batchSize {
					return nil, nil
				}
				n++
				idx++

				return &token2.UnspentTokenInWallet{
					Id:       token2.ID{TxId: "tx", Index: idx},
					Type:     "ABC",
					Quantity: "1",
				}, nil
			}

			return it, nil
		}

		return f
	}

	t.Run("TokenIterationLimit", func(t *testing.T) {
		fetcher := makeBatchFetcher(20)
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(false, nil) // every token appears locked by others

		// Limit iteration to 50 tokens; lock attempts unbounded. With batches of 20
		// locked tokens, the abort fires in the third reloaded batch (token 51).
		s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, mockLocker, 64, 50, 100000, 30*time.Second, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "9999999", "ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded max token iteration limit")
		assert.Contains(t, err.Error(), "50 tokens")
		// More than one fetch proves the counter survived cache swaps rather than
		// resetting with each reloaded iterator.
		assert.Greater(t, fetcher.UnspentTokensIteratorByCallCount(), 1,
			"limit must fire only after the selection crossed at least one cache reload")
	})

	t.Run("LockAttemptLimit", func(t *testing.T) {
		fetcher := makeBatchFetcher(20)
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(false, nil)

		// Limit lock attempts to 25; iteration unbounded. The abort fires in the
		// second reloaded batch (attempt 26).
		s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, mockLocker, 64, 100000, 25, 30*time.Second, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "9999999", "ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded max lock attempts")
		assert.Greater(t, fetcher.UnspentTokensIteratorByCallCount(), 1,
			"limit must fire only after the selection crossed at least one cache reload")
	})
}

type unitTestMockOwnerFilter struct {
	id string
}

func (f *unitTestMockOwnerFilter) ID() string {
	return f.id
}

func setupMetricsMocks() (*mocks.FakeProvider, *sherdlock.Metrics) {
	mockCounter := &mocks.FakeCounter{}
	mockCounter.WithReturns(mockCounter)
	mockHistogram := &mocks.FakeHistogram{}
	mockHistogram.WithReturns(mockHistogram)
	metricsProvider := &mocks.FakeProvider{}
	metricsProvider.NewCounterReturns(mockCounter)
	metricsProvider.NewHistogramReturns(mockHistogram)

	return metricsProvider, sherdlock.NewMetrics(metricsProvider)
}

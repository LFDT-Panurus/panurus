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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	metricsa "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectorUnit(t *testing.T) {
	_, metrics := setupMetricsMocks()

	t.Run("SelectSuccess", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)

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
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)

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
		s2 := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 2, metrics)
		err := s2.Close()
		require.NoError(t, err)

		_, _, err = s2.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "selector is already closed")
	})

	t.Run("FetcherError", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)

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
		s := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 100*time.Millisecond, 2, metrics)

		mockFetcher.UnspentTokensIteratorByStub = func(ctx context.Context, walletID string, tokenType token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
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
		s := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 100*time.Millisecond, 2, metrics)

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

		mockFetcher.UnspentTokensIteratorByStub = func(ctx context.Context, walletID string, tokenType token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
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

		shortBackoffS := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 1*time.Millisecond, 1, metrics)
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

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
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

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)
		_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "70", "ABC")
		require.Error(t, err)
		assert.True(t, errors.Is(err, token.SelectorRateLimited), "expected rate-limit error, got: %v", err)
		// Two tokens were locked before the rate-limited third lock aborted selection.
		assert.Equal(t, 3, mockLocker.TryLockCallCount())
		// The abort path must release the already-locked tokens.
		assert.Equal(t, 1, mockLocker.UnlockAllCallCount(), "selector must release locked tokens via UnlockAll on abort")
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

// setupNamedHistogramMocks builds a Metrics whose histograms are distinct fakes, keyed
// by metric name, so a test can assert on one histogram without the others' observations
// landing on the same fake.
func setupNamedHistogramMocks() (map[string]*mocks.FakeHistogram, *sherdlock.Metrics) {
	mockCounter := &mocks.FakeCounter{}
	mockCounter.WithReturns(mockCounter)
	histograms := map[string]*mocks.FakeHistogram{}
	metricsProvider := &mocks.FakeProvider{}
	metricsProvider.NewCounterReturns(mockCounter)
	metricsProvider.NewHistogramCalls(func(opts metricsa.HistogramOpts) metricsa.Histogram {
		h := &mocks.FakeHistogram{}
		h.WithReturns(h)
		histograms[opts.Name] = h

		return h
	})

	return histograms, sherdlock.NewMetrics(metricsProvider)
}

// TestDistinctTokensAttemptedObservedOncePerSelect pins the contract documented on
// Metrics.DistinctTokensAttempted and in docs/development/metrics.md: exactly one
// observation per Select() call, carrying the number of distinct tokens the whole call
// tried to lock. A StubbornSelector runs its inner selection once per backoff round, so
// observing inside that inner call would emit one sample per round, each counting only
// that round's tokens - inflating the sample count and understating per-call fan-out,
// which is the opposite of what #2395 needs the histogram for.
func TestDistinctTokensAttemptedObservedOncePerSelect(t *testing.T) {
	const retriesAfterBackoff = 2

	histograms, metrics := setupNamedHistogramMocks()

	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeTokenLocker{}
	// Two tokens, both always held by someone else: selection can never complete, so
	// the stubborn selector exhausts every backoff round and calls its inner selection
	// retriesAfterBackoff+1 times.
	mockFetcher.UnspentTokensIteratorByCalls(func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		it.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id: token2.ID{TxId: "tx1", Index: 0}, Type: "ABC", Quantity: "100",
		}, nil)
		it.NextReturnsOnCall(1, &token2.UnspentTokenInWallet{
			Id: token2.ID{TxId: "tx2", Index: 0}, Type: "ABC", Quantity: "100",
		}, nil)
		it.NextReturns(nil, nil)

		return it, nil
	})
	mockLocker.TryLockReturns(false, driver.ErrTokenAlreadyLocked)

	s := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, time.Millisecond, retriesAfterBackoff, metrics)
	_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.Error(t, err)

	// Precondition: the inner selection really did run more than once, otherwise this
	// test would pass even with the observation left in the per-attempt path.
	require.Greater(t, mockFetcher.UnspentTokensIteratorByCallCount(), retriesAfterBackoff,
		"expected the stubborn selector to retry, so that per-attempt observation would be visible")

	attempted := histograms["distinct_tokens_attempted"]
	require.NotNil(t, attempted, "distinct_tokens_attempted histogram was never created")
	require.Equal(t, 1, attempted.ObserveCallCount(), "DistinctTokensAttempted must be observed once per Select() call")
	// The count is an exact small integer, so an epsilon comparison would be noise.
	assert.InDelta(t, 2, attempted.ObserveArgsForCall(0), 0, "both distinct tokens must be counted once each, across all retries")
}

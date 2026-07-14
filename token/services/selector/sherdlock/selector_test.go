/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"testing"
	"time"

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
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)

		mockIt := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		mockIt.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: "100",
		}, nil)
		mockIt.NextReturnsOnCall(1, nil, nil)

		mockFetcher.UnspentTokensIteratorByReturns(mockIt, nil)
		mockLocker.TryLockReturns(true)

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
		mockLocker.TryLockReturnsOnCall(0, false)
		mockLocker.TryLockReturnsOnCall(1, true)

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
		mockLocker.TryLockReturns(false)

		shortBackoffS := sherdlock.NewStubbornSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, 1*time.Millisecond, 1, metrics)
		_, _, err := shortBackoffS.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.Error(t, err)
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

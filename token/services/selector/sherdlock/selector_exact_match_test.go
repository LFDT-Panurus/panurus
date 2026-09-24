/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTokenIterator returns a fresh FakeIterator that yields the given tokens once and
// then nil, so a fetcher stub can hand out an independent iterator on every call.
func newTokenIterator(toks ...*token2.UnspentTokenInWallet) *mocks.FakeIterator[*token2.UnspentTokenInWallet] {
	it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
	i := 0
	it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
		if i >= len(toks) {
			return nil, nil
		}
		t := toks[i]
		i++

		return t, nil
	}

	return it
}

func tok(txID, amount string) *token2.UnspentTokenInWallet {
	return &token2.UnspentTokenInWallet{
		Id:       token2.ID{TxId: txID, Index: 0},
		Type:     "ABC",
		Quantity: amount,
	}
}

func TestExactMatchSelectionUnit(t *testing.T) {
	_, metrics := setupMetricsMocks()

	t.Run("SingleTokenExactHitAvoidsChange", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Wallet holds 30, 40, 100, 70; request is 100. The exact 100 token exists.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("exact", "100"), tok("t70", "70")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 1, "exact match should select a single token, not overshoot")
		assert.Equal(t, "exact", tokens[0].TxId, "the token equal to the request must be chosen")
		assert.Equal(t, "100", sum.Decimal(), "sum must equal the request exactly (no change)")
		// Only the exact token is locked; the greedy walk never runs.
		assert.Equal(t, 1, mockLocker.TryLockCallCount())
	})

	t.Run("NoExactMatchFallsThroughToGreedy", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Wallet holds 30, 40, 70; request 100. No single token equals 100, so the
		// pre-search misses and the greedy walk runs, overshooting to 140.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("t70", "70")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Len(t, tokens, 3, "greedy walk selects until the sum reaches the target")
		assert.Equal(t, "140", sum.Decimal(), "greedy result overshoots, unchanged by the pre-search")
	})

	t.Run("ExactTokenLockedByAnotherFallsThrough", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("exact", "100"), tok("t70", "70")), nil
		}
		// The exact 100 token is held by someone else; everything else is lockable.
		mockLocker.TryLockStub = func(_ context.Context, id *token2.ID, _ string) (bool, error) {
			if id.TxId == "exact" {
				return false, nil
			}

			return true, nil
		}

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Equal(t, "140", sum.Decimal(), "unlockable exact token must not block the greedy fallback")
		assert.Len(t, tokens, 3)
	})

	t.Run("DisabledByDefaultRunsGreedy", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("exact", "100"), tok("t30", "30"), tok("t40", "40")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		// No WithExactMatch option: pre-search never runs, so the fetcher is queried
		// once (the greedy lazy refresh) rather than twice.
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Equal(t, "100", sum.Decimal())
		assert.Len(t, tokens, 1)
		assert.Equal(t, 1, mockFetcher.UnspentTokensIteratorByCallCount(), "pre-search must not fetch when disabled")
	})

	t.Run("MultipleExactCandidatesSelectOne", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Two tokens both equal to the request: exactly one should be selected.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("a", "100"), tok("b", "100"), tok("t70", "70")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 1)
		assert.Equal(t, "100", sum.Decimal())
		assert.Contains(t, []string{"a", "b"}, tokens[0].TxId)
		assert.Equal(t, 1, mockLocker.TryLockCallCount())
	})
}

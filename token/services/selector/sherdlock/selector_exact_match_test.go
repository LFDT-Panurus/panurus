/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"strconv"
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

	t.Run("ExceedsScanCapFallsThroughToGreedy", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// 257 candidates (> maxScanCandidates = 256): the pre-search must give up
		// and let the greedy walk run. The lone exact-amount token ("exact" = 100)
		// is placed last, so had the pre-search run it would have returned that
		// single token; the greedy walk instead accumulates 100 of the leading "1"
		// tokens. A 100-token result therefore proves the cap forced the fallback.
		toks := make([]*token2.UnspentTokenInWallet, 0, 257)
		for i := range 256 {
			toks = append(toks, tok("ones-"+strconv.Itoa(i), "1"))
		}
		toks = append(toks, tok("exact", "100"))
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(toks...), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Equal(t, "100", sum.Decimal())
		assert.Len(t, tokens, 100, "cap exceeded: pre-search must fall through to the greedy walk")
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

// pairSum returns the total of the two selected tokens' TxId-encoded amounts by looking
// them up in amountByID, so a test can assert a returned pair actually sums to the request.
func pairSum(t *testing.T, ids []*token2.ID, amountByID map[string]int) int {
	t.Helper()
	total := 0
	for _, id := range ids {
		amount, ok := amountByID[id.TxId]
		require.True(t, ok, "unexpected token %s in selection", id.TxId)
		total += amount
	}

	return total
}

func TestExactMatchPairSelectionUnit(t *testing.T) {
	_, metrics := setupMetricsMocks()

	t.Run("PairExactMatchAvoidsChange", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// The canonical #2404 example: wallet [30,40,70,200], request 100. Greedy would
		// pick {30,40,70}=140 (change 40); the k=2 pre-search must pick the pair {30,70}.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("t70", "70"), tok("t200", "200")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 2, "the completing pair must be selected, not the 3-input greedy overshoot")
		assert.Equal(t, "100", sum.Decimal(), "the pair sums to the request exactly (no change)")
		assert.ElementsMatch(t, []string{"t30", "t70"}, []string{tokens[0].TxId, tokens[1].TxId})
		// Exactly the two tokens of the pair are locked; the greedy walk never runs.
		assert.Equal(t, 2, mockLocker.TryLockCallCount())
	})

	t.Run("SingleTokenPreferredOverPair", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// A single 100 and a pair 50+50 both complete the request; the single (one input)
		// must win because k=1 is tried before k=2.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("a50", "50"), tok("b50", "50"), tok("exact", "100")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 1, "the single completing token must be preferred over a pair")
		assert.Equal(t, "exact", tokens[0].TxId)
		assert.Equal(t, "100", sum.Decimal())
		assert.Equal(t, 1, mockLocker.TryLockCallCount())
	})

	t.Run("NoCompletingPairFallsThroughToGreedy", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// No single token and no pair sums to 100 (30+40=70, 30+200=230, 40+200=240), so
		// the pre-search misses and the greedy walk runs, overshooting to 270.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("t200", "200")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Len(t, tokens, 3, "greedy walk selects until the sum reaches the target")
		assert.Equal(t, "270", sum.Decimal())
	})

	t.Run("PartialPairLockUnwindsAndFallsThrough", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// The only completing pair is {30,70}, but t70 is held by another process. The
		// pre-search must lock t30, fail on t70, release t30 via UnlockAll, and fall
		// through to the greedy walk with no lock left behind.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t70", "70"), tok("t40", "40"), tok("t200", "200")), nil
		}
		mockLocker.TryLockStub = func(_ context.Context, id *token2.ID, _ string) (bool, error) {
			if id.TxId == "t70" {
				return false, nil
			}

			return true, nil
		}

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		// The half-locked pair is unwound exactly once, before the greedy fallback runs.
		assert.Equal(t, 1, mockLocker.UnlockAllCallCount(), "a partially locked pair must be released via UnlockAll")
		// Greedy then completes over the lockable tokens {30,40,200}=270 (t70 stays skipped).
		assert.Len(t, tokens, 3)
		assert.Equal(t, "270", sum.Decimal())
	})

	t.Run("MultiplePartialPairLocksUnwindOncePerFailure", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Two completing pairs exist for request 100: {10,90} and {20,80}. In each pair the
		// larger token (the one locked second) is held by another process, so every pair
		// locks its first token, fails on its second, and must unwind — twice in total. The
		// smaller tokens {10,20} plus the non-pairing {50,60} are lockable and sum to 140,
		// so the greedy fallback completes without hitting its own error/unlock path. This
		// isolates the count to the pre-search: UnlockAll must be called exactly twice, once
		// per failed second lock, with no cross-pair over-release.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t10", "10"), tok("t20", "20"), tok("t50", "50"), tok("t60", "60"), tok("t80", "80"), tok("t90", "90")), nil
		}
		// The larger token of each completing pair is contended; everything else locks.
		mockLocker.TryLockStub = func(_ context.Context, id *token2.ID, _ string) (bool, error) {
			if id.TxId == "t80" || id.TxId == "t90" {
				return false, nil
			}

			return true, nil
		}

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		// The heart of the test: one unwind per failed pair, never more.
		assert.Equal(t, 2, mockLocker.UnlockAllCallCount(), "each failed pair must unwind exactly once (no cross-pair over-release)")
		// Greedy then completes over the lockable tokens {10,20,50,60}=140; t80/t90 stay skipped.
		assert.Len(t, tokens, 4)
		assert.Equal(t, "140", sum.Decimal())
	})

	t.Run("ExactMatchInputsOneNeverUsesPair", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// The pair {30,70} completes the request, but with k=1 it must be ignored and the
		// greedy walk must overshoot to 140, exactly as WithExactMatch(true) behaves.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("t70", "70")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(1))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Len(t, tokens, 3, "k=1 must not combine a pair; greedy overshoots")
		assert.Equal(t, "140", sum.Decimal())
	})

	t.Run("MultipleCompletingPairsSelectOneValidPair", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Several pairs complete 100: {20,80}, {30,70}, {50,50}. Exactly one valid pair
		// must be selected (which one is randomised to spread contention).
		amountByID := map[string]int{"a20": 20, "b30": 30, "c50": 50, "d50": 50, "e70": 70, "f80": 80}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("a20", "20"), tok("b30", "30"), tok("c50", "50"), tok("d50", "50"), tok("e70", "70"), tok("f80", "80")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 2)
		assert.Equal(t, "100", sum.Decimal())
		assert.Equal(t, 100, pairSum(t, tokens, amountByID), "the two selected tokens must actually sum to the request")
		assert.Equal(t, 2, mockLocker.TryLockCallCount())
	})
}

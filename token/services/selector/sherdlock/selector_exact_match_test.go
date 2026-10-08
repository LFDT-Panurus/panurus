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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
				return false, driver.ErrTokenAlreadyLocked
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

	t.Run("LargeWalletStillFindsExactMatch", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// A dust-heavy wallet (far larger than any fixed candidate cap) with one exact-amount
		// token placed last. The pre-search scans the whole set with no size cap, so it still
		// finds and locks the exact token rather than bailing to the greedy walk — a large,
		// dust-heavy wallet is exactly the case change-avoidance targets. The greedy walk
		// would instead have accumulated 100 of the leading "1" tokens.
		toks := make([]*token2.UnspentTokenInWallet, 0, 1001)
		for i := range 1000 {
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
		require.Len(t, tokens, 1, "the exact-amount token must be found even in a large wallet")
		assert.Equal(t, "exact", tokens[0].TxId)
		assert.Equal(t, "100", sum.Decimal())
		assert.Equal(t, 1, mockLocker.TryLockCallCount(), "only the exact token is locked")
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

// TestExactMatchMetricsAndContention pins the #2395 metrics/logging contract for the
// pre-search: a change-free hit is still counted in distinct_tokens_attempted, the three
// selection_exact_match_* counters move independently, lost lock races the pre-search
// loses increment lock_conflicts_total while a real store error does not, and a miss hands
// its snapshot to the greedy walk instead of fetching the wallet twice.
func TestExactMatchMetricsAndContention(t *testing.T) {
	owner := &unitTestMockOwnerFilter{id: "alice"}

	t.Run("hit moves attempt+hit only, no miss or conflict", func(t *testing.T) {
		counters, metrics := setupNamedCounterMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("exact", "100")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))
		tokens, _, err := s.Select(t.Context(), owner, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 1)

		assert.Equal(t, 1, counters["selection_exact_match_attempts_total"].AddCallCount())
		assert.Equal(t, 1, counters["selection_exact_match_hits_total"].AddCallCount())
		assert.Equal(t, 0, counters["selection_exact_match_misses_total"].AddCallCount())
		assert.Equal(t, 0, counters["lock_conflicts_total"].AddCallCount(), "a clean lock is not a conflict")
	})

	t.Run("change-free hit is still recorded in distinct_tokens_attempted", func(t *testing.T) {
		histograms, metrics := setupNamedHistogramMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("exact", "100")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))
		_, _, err := s.Select(t.Context(), owner, "100", "ABC")
		require.NoError(t, err)

		h := histograms["distinct_tokens_attempted"]
		require.NotNil(t, h, "distinct_tokens_attempted histogram was never created")
		require.Equal(t, 1, h.ObserveCallCount(), "a change-free hit must record its fan-out, not vanish from the histogram")
		assert.InDelta(t, 1, h.ObserveArgsForCall(0), 0, "the single locked token is the one distinct attempt")
	})

	t.Run("pre-search skips a locked exact token, locks the next, and counts the conflict", func(t *testing.T) {
		counters, metrics := setupNamedCounterMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Two exact candidates: the first the shuffle tries loses the race, the second is
		// acquired, so the result is still a single change-free token. The greedy walk never
		// runs, so the one conflict counted can only come from the pre-search itself.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("a", "100"), tok("b", "100"), tok("t70", "70")), nil
		}
		var calls int
		mockLocker.TryLockStub = func(context.Context, *token2.ID, string) (bool, error) {
			calls++
			if calls == 1 {
				return false, driver.ErrTokenAlreadyLocked
			}

			return true, nil
		}

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))
		tokens, sum, err := s.Select(t.Context(), owner, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 1, "the second exact candidate completes the change-free selection")
		assert.Contains(t, []string{"a", "b"}, tokens[0].TxId)
		assert.Equal(t, "100", sum.Decimal())
		assert.Equal(t, 2, mockLocker.TryLockCallCount(), "exactly one retry after the lost race")

		assert.Equal(t, 1, counters["selection_exact_match_hits_total"].AddCallCount())
		assert.Equal(t, 0, counters["selection_exact_match_misses_total"].AddCallCount())
		assert.Equal(t, 1, counters["lock_conflicts_total"].AddCallCount(),
			"the pre-search's own lost lock race must be counted (greedy never ran)")
	})

	t.Run("pre-search store error is not counted as a lock conflict", func(t *testing.T) {
		counters, metrics := setupNamedCounterMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// A single exact token whose lock fails with a real store error, not a lock conflict.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("exact", "100")), nil
		}
		mockLocker.TryLockReturns(false, errors.New("db connection refused"))

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))
		_, _, err := s.Select(t.Context(), owner, "100", "ABC")
		require.Error(t, err, "a store outage that blocks every lock cannot complete the selection")

		assert.Equal(t, 0, counters["lock_conflicts_total"].AddCallCount(),
			"a real store error must not inflate lock_conflicts_total")
		assert.Equal(t, 1, counters["selection_exact_match_misses_total"].AddCallCount())
	})

	t.Run("a close landing mid-scan stops the pre-search before it locks", func(t *testing.T) {
		_, metrics := setupMetricsMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		var s *sherdlock.Selector
		// The iterator closes the selector just as the scan ends, standing in for a concurrent
		// Manager.Close(txID) that lands between the pre-search scan and its first lock attempt.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
			calls := 0
			it.NextStub = func() (*token2.UnspentTokenInWallet, error) {
				calls++
				if calls == 1 {
					return tok("exact", "100"), nil
				}
				_ = s.Close()

				return nil, nil
			}

			return it, nil
		}
		mockLocker.TryLockReturns(true, nil)

		s = sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))
		_, _, err := s.Select(t.Context(), owner, "100", "ABC")
		require.Error(t, err, "a selection on a selector closed mid-flight must fail, not succeed")
		assert.Equal(t, 0, mockLocker.TryLockCallCount(),
			"the pre-search must not acquire a lock for a selector that was closed during the scan")
	})

	t.Run("miss hands its snapshot to the greedy walk instead of re-fetching", func(t *testing.T) {
		_, metrics := setupMetricsMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// No single token equals 100, so the pre-search misses and the greedy walk overshoots.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40"), tok("t70", "70")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))
		_, sum, err := s.Select(t.Context(), owner, "100", "ABC")
		require.NoError(t, err)
		assert.Equal(t, "140", sum.Decimal(), "greedy fallback overshoots, as before")
		assert.Equal(t, 1, mockFetcher.UnspentTokensIteratorByCallCount(),
			"on a miss the pre-search snapshot is reused: the wallet is fetched once, not twice")
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
		// pre-search must lock t30, fail on t70, release only t30 (per-token, not tx-wide),
		// and fall through to the greedy walk with no lock left behind.
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
		// The half-locked pair is unwound by releasing exactly its own token, once, and
		// never tx-wide: UnlockAll must not be used (it would drop other actions' inputs).
		require.Equal(t, 1, mockLocker.UnlockCallCount(), "the partially locked pair must release its own token")
		assert.Equal(t, 0, mockLocker.UnlockAllCallCount(), "the pre-search must not unlock the whole transaction")
		_, releasedID, _ := mockLocker.UnlockArgsForCall(0)
		assert.Equal(t, "t30", releasedID.TxId, "only the first (locked) token of the failed pair must be released")
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
		// isolates the count to the pre-search: Unlock must be called exactly twice, once
		// per failed second lock, each releasing that pair's own first token and nothing else.
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
		// The heart of the test: one per-token unwind per failed pair, never tx-wide.
		require.Equal(t, 2, mockLocker.UnlockCallCount(), "each failed pair must release exactly its own token")
		assert.Equal(t, 0, mockLocker.UnlockAllCallCount(), "the pre-search must not unlock the whole transaction")
		released := make([]string, 0, 2)
		for i := range 2 {
			_, id, _ := mockLocker.UnlockArgsForCall(i)
			released = append(released, id.TxId)
		}
		// The first (smaller) token of each pair is released; order depends on the pair shuffle.
		assert.ElementsMatch(t, []string{"t10", "t20"}, released, "only each pair's own locked token is released")
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

	t.Run("OverflowingProbeDoesNotAbortScan", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Precision 8 (max representable 255). Wallet [50,50,250], request 100. The
		// pre-search sorts candidates ascending, so its first two-pointer probe is
		// smallest+largest = 50+250 = 300, which overflows the precision. An overflow
		// must be treated as "pair too large" (shrink the top), not as a reason to abort:
		// the valid {50,50} pair below the 250 token must still be found. The 250 token
		// is yielded first so that if the scan aborted, the greedy fallback would select
		// it alone and mint a 150 change output — making a 2-token/sum-100 result proof
		// the pre-search (not the greedy walk) completed the selection.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("c250", "250"), tok("a50", "50"), tok("b50", "50")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 8, metrics, sherdlock.WithExactMatchInputs(2))

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 2, "the {50,50} pair must be found despite the overflowing first probe")
		assert.Equal(t, "100", sum.Decimal(), "the pair sums to the request exactly (no change)")
		assert.ElementsMatch(t, []string{"a50", "b50"}, []string{tokens[0].TxId, tokens[1].TxId})
		assert.Equal(t, 2, mockLocker.TryLockCallCount(), "only the pair is locked; the greedy walk never runs")
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

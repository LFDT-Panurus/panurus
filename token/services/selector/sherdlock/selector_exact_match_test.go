/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
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

	t.Run("InsufficientFundsAfterSeededGreedyDoesNotSpin", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// Wallet holds 30, 40 (sum 70); request 100. No single token equals 100, so the
		// pre-search misses and seeds the greedy walk with the full snapshot. The wallet is
		// genuinely short: the greedy walk locks both tokens, exhausts the seeded cache, and
		// must conclude insufficient funds.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("t40", "40")), nil
		}
		// Model the real locker: TokenLocks is PRIMARY KEY(tx_id, idx), so a second attempt to
		// lock a token this selector already holds returns ErrTokenAlreadyLocked, not success.
		// Without this, a self-relock would wrongly look like a fresh lock and hide the spin.
		locked := map[token2.ID]bool{}
		mockLocker.TryLockStub = func(_ context.Context, id *token2.ID, _ string) (bool, error) {
			if locked[*id] {
				return false, driver.ErrTokenAlreadyLocked
			}
			locked[*id] = true

			return true, nil
		}

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.Error(t, err)
		assert.Nil(t, tokens)
		// The wallet is short, not contended: the result must be InsufficientFunds, never
		// SufficientButLockedFunds. The latter is the symptom of the seeded cache leaving
		// tokensLockedByOthersExist initialized to true.
		assert.True(t, errors.Is(err, token.SelectorInsufficientFunds),
			"a genuinely short wallet must report insufficient funds, got %v", err)
		assert.False(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
			"must not misreport a short wallet as locked funds")
		// The pre-search fetches once and seeds the greedy walk; a correct run reuses that
		// snapshot and concludes. Re-fetching means the walk re-entered the refresh branch to
		// re-lock tokens it already holds — the spin this guards against.
		assert.Equal(t, 1, mockFetcher.UnspentTokensIteratorByCallCount(),
			"insufficient funds must be concluded from the seeded snapshot, not by re-fetching and re-locking")
	})

	t.Run("ContendedExactTokenReportsLockedFunds", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// The only token that can cover the request equals it exactly, but another process
		// holds its lock for the whole call. The pre-search loses the race and seeds the
		// greedy walk, which cannot lock it either: the funds exist but are locked. This must
		// exhaust the immediate-retry budget and report SufficientButLockedFunds — the
		// complement of InsufficientFundsAfterSeededGreedyDoesNotSpin above, which must stay
		// InsufficientFunds. Together they pin both verdicts the seeded flag drives.
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("exact", "100")), nil
		}
		mockLocker.TryLockReturns(false, driver.ErrTokenAlreadyLocked)

		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics, sherdlock.WithExactMatch(true))

		tokens, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.Error(t, err)
		assert.Nil(t, tokens)
		assert.True(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
			"a contended exact token whose funds exist but are locked must report locked funds, got %v", err)
		assert.False(t, errors.Is(err, token.SelectorInsufficientFunds),
			"the wallet can cover the request once unlocked, so this is not insufficient funds")
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
		counters, metrics := setupNamedCounterMocks()
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		mockFetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("exact", "100"), tok("t30", "30"), tok("t40", "40")), nil
		}
		mockLocker.TryLockReturns(true, nil)

		// No WithExactMatch option: the pre-search never runs.
		s := sherdlock.NewSelector(sherdlock.Logger(), mockFetcher, mockLocker, 64, metrics)

		_, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		req, err := token2.ToQuantity("100", 64)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, sum.Cmp(req), 0, "greedy selection must cover the request")
		// The pre-search leaves a deterministic trace even though the greedy walk's randomized
		// sufficiency window makes the selected token count non-deterministic: with the option
		// off it never records an attempt, and the fetcher is queried once (the greedy refresh)
		// rather than twice (pre-search fetch + greedy refetch).
		assert.Equal(t, 0, counters["selection_exact_match_attempts_total"].AddCallCount(), "pre-search must not run when disabled")
		assert.Equal(t, 1, mockFetcher.UnspentTokensIteratorByCallCount(), "pre-search must not fetch when disabled")
	})

	t.Run("LargeWalletStillFindsExactMatch", func(t *testing.T) {
		mockFetcher := &mocks.FakeTokenFetcher{}
		mockLocker := &mocks.FakeTokenLocker{}
		// A dust-heavy wallet (far more than the old 256 cap) with one exact-amount token
		// placed last. The pre-search scans linearly with no sort, so it still finds and
		// locks the exact token rather than bailing to the greedy walk — a large, dust-heavy
		// wallet is exactly the case change-avoidance targets. The greedy walk would instead
		// have accumulated 100 of the leading "1" tokens.
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

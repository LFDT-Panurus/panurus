/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
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

// TestSelectorFastFail_PartiallyFilledRequest pins that the sum-aware fast fail
// (selectInternal's HasEnoughSpendableTokens check) still fires once this call has already
// won some tokens. HasEnoughSpendableTokens deliberately ignores locks, so the wallet total
// it reports already includes whatever this call locked: comparing it against the *remaining*
// amount (quantity - sum) would degenerate into `total >= total - sum`, true as soon as
// anything at all was selected, and the check could never fire on a partially-fillable
// wallet — exactly the case it exists for. The comparison must therefore be against the full
// requested quantity.
//
// The "nothing selected yet" subtest is the control: it is the shape the pre-existing Phase 6
// coverage used, and it passed either way, which is why the defect went unnoticed.
func TestSelectorFastFail_PartiallyFilledRequest(t *testing.T) {
	const precision = 64
	_, metrics := setupMetricsMocks()

	tests := []struct {
		name       string
		quantities []string
	}{
		{name: "partially fillable wallet", quantities: []string{"5"}},
		{name: "nothing selected yet", quantities: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := newBalanceFetcher(precision, test.quantities...)
			locker := &recordingLocker{fetcher: fetcher}

			s := sherdlock.NewSelector(sherdlock.Logger(), fetcher, locker, precision, metrics)
			_, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "10", "ABC")

			require.Error(t, err)
			assert.True(t, errors.Is(err, token.SelectorInsufficientFunds),
				"a wallet whose whole balance is below the request, with nothing locked by anyone else, "+
					"must be reported as insufficient funds; got: %v", err)
			assert.False(t, errors.Is(err, token.SelectorSufficientButLockedFunds),
				"nothing is locked by another process here, so SelectorSufficientButLockedFunds is the wrong class")
			assert.LessOrEqual(t, int(fetcher.fetches.Load()), 2,
				"the fast fail exists to avoid burning the immediate-retry budget on an unpayable wallet")
			assert.Equal(t, []string{"10"}, fetcher.hasEnoughTargets(),
				"the check must be made against the full requested amount, not the remaining one: the "+
					"lock-ignoring wallet total already includes the tokens this call selected")
		})
	}
}

type unitTestMockOwnerFilter struct {
	id string
}

func (f *unitTestMockOwnerFilter) ID() string {
	return f.id
}

// balanceFetcher is a sherdlock.TokenFetcher over a fixed wallet, modelling the two
// production query semantics the selector depends on:
//   - UnspentTokensIteratorBy applies the anti-join (#2395 phase 4a): a token already locked
//     is hidden from every subsequent fetch, and the remaining ones come back ascending by
//     amount, as buildSpendableTokensIteratorByQuery's ORDER BY guarantees;
//   - HasEnoughSpendableTokens deliberately *ignores* locks (see its Godoc in
//     token/services/storage/db/sql/common/tokens.go), i.e. it answers over the wallet's full
//     balance, including tokens the caller itself has already locked — a lock is a row in
//     TokenLocks, the token stays spendable in Tokens until the transaction commits.
//
// It records the amounts it was asked about so a test can assert what the selector compared
// the balance against.
type balanceFetcher struct {
	mu                sync.Mutex
	tokens            []*token2.UnspentTokenInWallet
	locked            map[token2.ID]bool
	targets           []string
	fetches           atomic.Int32
	precision         uint64
	keepLockedVisible bool
}

// newBalanceFetcher builds a balanceFetcher holding one token per quantity, named tx-0,
// tx-1, ... in the order given (which must be ascending, to match the production ORDER BY).
func newBalanceFetcher(precision uint64, quantities ...string) *balanceFetcher {
	f := &balanceFetcher{locked: map[token2.ID]bool{}, precision: precision}
	for i, q := range quantities {
		f.tokens = append(f.tokens, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: fmt.Sprintf("tx-%d", i), Index: 0},
			Type:     "ABC",
			Quantity: q,
		})
	}

	return f
}

// markLocked records that id is now locked, so the anti-join hides it from later fetches.
func (f *balanceFetcher) markLocked(id token2.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locked[id] = true
}

// hasEnoughTargets returns the decimal amounts HasEnoughSpendableTokens was asked about, in
// call order.
func (f *balanceFetcher) hasEnoughTargets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.targets...)
}

// UnspentTokensIteratorBy returns the wallet's still-unlocked tokens, ascending by amount.
func (f *balanceFetcher) UnspentTokensIteratorBy(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
	f.fetches.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()

	visible := make([]*token2.UnspentTokenInWallet, 0, len(f.tokens))
	for _, t := range f.tokens {
		if !f.keepLockedVisible && f.locked[t.Id] {
			continue
		}
		visible = append(visible, t)
	}

	return &sliceIterator{items: visible}, nil
}

// HasAnySpendableTokens reports whether the wallet holds any token at all, locks included.
func (f *balanceFetcher) HasAnySpendableTokens(context.Context, string, token2.Type) (bool, error) {
	return len(f.tokens) > 0, nil
}

// HasEnoughSpendableTokens reports whether the wallet's whole balance, locks included, is at
// least target.
func (f *balanceFetcher) HasEnoughSpendableTokens(_ context.Context, _ string, _ token2.Type, target *big.Int) (bool, error) {
	sum := big.NewInt(0)
	for _, t := range f.tokens {
		q, err := token2.ToQuantity(t.Quantity, f.precision)
		if err != nil {
			return false, err
		}
		sum.Add(sum, q.ToBigInt())
	}

	f.mu.Lock()
	f.targets = append(f.targets, target.String())
	f.mu.Unlock()

	return sum.Cmp(target) >= 0, nil
}

// sliceIterator is a sherdlock.Iterator over a fixed slice, signalling exhaustion the way
// sherdlock's contract requires (nil element, nil error — see bucketedIterator.Next).
type sliceIterator struct {
	items []*token2.UnspentTokenInWallet
	pos   int
}

func (s *sliceIterator) Next() (*token2.UnspentTokenInWallet, error) {
	if s.pos >= len(s.items) {
		return nil, nil
	}
	t := s.items[s.pos]
	s.pos++

	return t, nil
}

func (s *sliceIterator) Close() {}

// recordingLocker is a sherdlock.TokenLocker that grants every lock and tells the fetcher
// about it, so the fetcher's anti-join hides the token from later fetches, as production does.
type recordingLocker struct {
	fetcher *balanceFetcher
	locked  []token2.ID
}

func (l *recordingLocker) TryLock(_ context.Context, id *token2.ID, _ string) (bool, error) {
	l.locked = append(l.locked, *id)
	l.fetcher.markLocked(*id)

	return true, nil
}

func (l *recordingLocker) UnlockAll(context.Context) error { return nil }

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

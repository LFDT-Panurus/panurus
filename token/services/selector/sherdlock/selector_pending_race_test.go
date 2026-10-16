/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"math/big"
	"strconv"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/require"
)

// alwaysContendedLocker reports every token as already locked by someone else, which is the
// shape that keeps nextCandidate's lookahead buffer churning: every peeked window is
// re-buffered and re-dequeued until the retry budget runs out.
type alwaysContendedLocker struct{}

func (alwaysContendedLocker) TryLock(context.Context, *token2.ID, string) (bool, error) {
	return false, driver.ErrTokenAlreadyLocked
}

func (alwaysContendedLocker) UnlockAll(context.Context) error { return nil }

// TestSelector_ConcurrentSelectAndCloseDoNotRaceOnPendingBuffer pins the memory-safety
// contract of the lookahead buffer: s.pending is shared mutable state on the Selector, like
// s.cache, and the manager hands out one cached Selector per transaction.ID, which any of its
// callers may Close from another goroutine at any time. Every read and write therefore goes
// through dequeue/requeue/dropPending or swapCache/Close, all of which hold s.mu.
//
// Without that guard this test reports a data race under -race: the Select goroutines mutate
// the slice header (s.pending = s.pending[1:], append(...)) while a concurrent Close writes it.
// It is a race-detector test, so it asserts only that nothing panics and that every call
// terminates - a closed selector legitimately reports "already closed", and a contended one
// legitimately reports locked funds.
func TestSelector_ConcurrentSelectAndCloseDoNotRaceOnPendingBuffer(t *testing.T) {
	// Several individually-sufficient tokens per fetch, so nextCandidate always assembles a
	// window and buffers everything it did not return. A single token would leave the buffer
	// empty and the race unobservable.
	fetcher := &mockTokenFetcher{
		unspentTokensIteratorByFunc: func(context.Context, string, token2.Type) (Iterator[*token2.UnspentTokenInWallet], error) {
			tokens := make([]*token2.UnspentTokenInWallet, 0, 8)
			for i := range 8 {
				tokens = append(tokens, &token2.UnspentTokenInWallet{
					Id:       token2.ID{TxId: "tx", Index: uint64(i)},
					Type:     "USD",
					Quantity: "100",
				})
			}

			return iterators.Slice(tokens), nil
		},
		hasEnoughSpendableTokensFunc: func(context.Context, string, token2.Type, *big.Int) (bool, error) {
			return true, nil
		},
	}

	sel := NewSelector(logger, fetcher, alwaysContendedLocker{}, 64, NewMetrics(&disabled.Provider{}))

	const selectors = 4
	var wg sync.WaitGroup
	wg.Add(selectors + 1)
	for range selectors {
		go func() {
			defer wg.Done()
			// The outcome is irrelevant: either the selector was closed under us, or the
			// permanently contended locker exhausted the retry budget.
			_, _, _ = sel.Select(t.Context(), &ownerFilter{id: "wallet1"}, "50", "USD")
		}()
	}
	go func() {
		defer wg.Done()
		_ = sel.Close()
	}()
	wg.Wait()

	require.True(t, sel.isClosed(), "Close must leave the selector closed whatever the Select calls did")
}

// recordingWindowLocker claims every candidate it is offered and records the size of each
// window, so a test can assert on how many candidates one claim covered.
type recordingWindowLocker struct {
	mu      sync.Mutex
	windows []int
}

func (l *recordingWindowLocker) TryLock(context.Context, *token2.ID, string) (bool, error) {
	return true, nil
}

func (l *recordingWindowLocker) TryLockBatch(_ context.Context, tokenIDs []*token2.ID, _ string) (driver.BatchLockOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.windows = append(l.windows, len(tokenIDs))

	return driver.BatchLockOutcome{Won: tokenIDs}, nil
}

func (l *recordingWindowLocker) UnlockAll(context.Context) error { return nil }

func (l *recordingWindowLocker) sizes() []int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]int(nil), l.windows...)
}

// TestSelector_BatchWindowIsCapped pins maxBatchWindow. The window grows until it covers the
// outstanding amount, which is unbounded in the number of tokens: a wallet of unit-value dust
// paying a large amount needs one input per unit. The Postgres claim binds 2n+5 parameters and
// the protocol refuses more than 65535, so an uncapped window becomes a store error that the
// retry budget then burns through, failing a selection the single-token path would have
// completed. The cap must not change the outcome - the outer loop claims another window - so
// this asserts both: no claim exceeds the cap, and the full amount is still selected.
func TestSelector_BatchWindowIsCapped(t *testing.T) {
	const dust = maxBatchWindow * 3

	fetcher := &mockTokenFetcher{
		unspentTokensIteratorByFunc: func(context.Context, string, token2.Type) (Iterator[*token2.UnspentTokenInWallet], error) {
			tokens := make([]*token2.UnspentTokenInWallet, 0, dust)
			for i := range dust {
				tokens = append(tokens, &token2.UnspentTokenInWallet{
					Id:       token2.ID{TxId: "dust", Index: uint64(i)},
					Type:     "USD",
					Quantity: "1",
				})
			}

			return iterators.Slice(tokens), nil
		},
		hasEnoughSpendableTokensFunc: func(context.Context, string, token2.Type, *big.Int) (bool, error) {
			return true, nil
		},
	}

	locker := &recordingWindowLocker{}
	sel := NewSelector(logger, fetcher, locker, 64, NewMetrics(&disabled.Provider{}))

	// Needs every unit-value token in the wallet, i.e. three full windows' worth.
	selected, sum, err := sel.Select(t.Context(), &ownerFilter{id: "wallet1"}, strconv.Itoa(dust), "USD")
	require.NoError(t, err)
	require.Len(t, selected, dust, "the cap must not reduce what gets selected")
	require.Equal(t, strconv.Itoa(dust), sum.Decimal())

	sizes := locker.sizes()
	require.NotEmpty(t, sizes, "the batch path must have been taken")
	for i, size := range sizes {
		require.LessOrEqual(t, size, maxBatchWindow,
			"claim %d covered %d candidates, over the %d cap: a large enough window exceeds Postgres's bind-parameter limit",
			i, size, maxBatchWindow)
	}
	require.Greater(t, len(sizes), 1,
		"a request needing more than one window must be served by several claims, not one oversized one")
}

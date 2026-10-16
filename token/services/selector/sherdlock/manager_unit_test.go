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
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerUnit(t *testing.T) {
	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeLocker{}
	_, metrics := setupMetricsMocks()

	mgr := sherdlock.NewManager(mockFetcher, mockLocker, 64, 0, 0, 0, 0, metrics)
	require.NotNil(t, mgr)

	t.Run("NewSelector", func(t *testing.T) {
		sel, err := mgr.NewSelector(transaction.ID("tx1"))
		require.NoError(t, err)
		assert.NotNil(t, sel)
	})

	t.Run("Close_NotFound", func(t *testing.T) {
		err := mgr.Close(transaction.ID("nonexistent"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Stop", func(t *testing.T) {
		require.NoError(t, mgr.Stop())
	})
}

// TestManagerForwardsExactMatchOption proves the WithExactMatch option reaches the selectors a
// Manager builds (the production wiring path config -> loader -> NewManager -> NewSherdSelector).
// The wallet is ordered so greedy first-fit overshoots (30 + 100 = 130, two tokens) while the
// exact-amount pre-search returns the single 100 token with zero change: the result alone tells
// whether the pre-search ran.
func TestManagerForwardsExactMatchOption(t *testing.T) {
	_, metrics := setupMetricsMocks()

	newFetcher := func() *mocks.FakeTokenFetcher {
		f := &mocks.FakeTokenFetcher{}
		f.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
			return newTokenIterator(tok("t30", "30"), tok("exact", "100"), tok("t70", "70")), nil
		}

		return f
	}

	t.Run("enabled forwards the option: pre-search avoids change", func(t *testing.T) {
		locker := &mocks.FakeLocker{}
		locker.LockReturns(nil)
		mgr := sherdlock.NewManager(newFetcher(), locker, 64, sherdlock.NoBackoff, 0, 0, 0, metrics, sherdlock.WithExactMatch(true))
		sel, err := mgr.NewSelector(transaction.ID("tx-on"))
		require.NoError(t, err)

		tokens, sum, err := sel.Select(context.Background(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		require.Len(t, tokens, 1, "exact-match pre-search must select the single 100 token")
		assert.Equal(t, "exact", tokens[0].TxId)
		assert.Equal(t, "100", sum.Decimal())
	})

	t.Run("default (no option): greedy walk overshoots", func(t *testing.T) {
		locker := &mocks.FakeLocker{}
		locker.LockReturns(nil)
		mgr := sherdlock.NewManager(newFetcher(), locker, 64, sherdlock.NoBackoff, 0, 0, 0, metrics)
		sel, err := mgr.NewSelector(transaction.ID("tx-off"))
		require.NoError(t, err)

		_, sum, err := sel.Select(context.Background(), &unitTestMockOwnerFilter{id: "alice"}, "100", "ABC")
		require.NoError(t, err)
		assert.Equal(t, "130", sum.Decimal(), "without the option the plain greedy first-fit must run")
	})
}

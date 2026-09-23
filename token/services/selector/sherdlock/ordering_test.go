/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSizeOrderedSelection_SmallestFit reproduces the exact scenario from #2395's phase 4b
// fix (see sherdlock.bucketedIterator's doc comment, fetcher.go: "why did a 1 CHF request grab
// a 200 CHF token instead of a same-size one"): a wallet holding one small token and one much
// larger one, asked for exactly the small amount. Before phase 4b, the fetcher had no ordering
// guarantee at all, so the large token could be selected just as easily as the small one,
// needlessly fragmenting it into a same-size token via a follow-up mint. This test wires a real
// lazyFetcher over testutils.MockQueryService (no container needed) rather than
// mocks.FakeTokenFetcher, specifically because it is bucketedIterator's ORDER-BY-preserving
// behavior under test here, not just the selector's own loop - a fake fetcher's canned
// iterator order would prove nothing about the real fetcher/DB contract this test exists to
// pin (buildSpendableTokensIteratorByQuery's ORDER BY, tokens.go:415).
func TestSizeOrderedSelection_SmallestFit(t *testing.T) {
	const walletID = "wallet-xavier"
	const tokenType = "EUR"

	qs := testutils.NewMockQueryService()
	addMockToken(qs, walletID, tokenType, "tx-small", "1")
	addMockToken(qs, walletID, tokenType, "tx-big", "200")
	qs.WarmupCache(walletID, tokenType)

	_, metrics := setupMetricsMocks()
	mockLocker := &mocks.FakeTokenLocker{}
	mockLocker.TryLockReturns(true, nil)

	s := sherdlock.NewSelector(sherdlock.Logger(), sherdlock.NewLazyFetcher(qs), mockLocker, testutils.TokenQuantityPrecision, metrics)

	tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: walletID}, "1", tokenType)
	require.NoError(t, err)
	require.Len(t, tokens, 1, "a 1 EUR request satisfiable by the 1 EUR token alone must never need the 200 EUR one too")
	assert.Equal(t, "tx-small", tokens[0].TxId, "expected the smallest-fitting token to be selected, not the hot 200 EUR one (#2395)")
	assert.Equal(t, "1", sum.Decimal())
}

// TestSizeOrderedSelection_SameAmountBucketShuffle pins the other half of bucketedIterator's
// contract: candidates of equal amount must still be shuffled against each other (so
// smallest-fit does not simply relocate all contention onto whichever equal-amount token
// happens to sort first), while the ascending ordering across distinct amounts is preserved.
// It repeatedly selects a single token satisfying a request smaller than any one of several
// equal-amount candidates, across independent Selector instances (each gets its own fresh
// permutation via lazyFetcher/bucketedIterator.NewPermutation), and asserts more than one of
// the candidates was picked across the run. With 5 equally likely candidates, the odds every
// one of 40 independent trials lands on the same candidate are astronomically small, so this
// is not a flaky assertion in practice.
func TestSizeOrderedSelection_SameAmountBucketShuffle(t *testing.T) {
	const walletID = "wallet-bucket"
	const tokenType = "EUR"
	const numCandidates = 5
	const numTrials = 40

	qs := testutils.NewMockQueryService()
	for i := range numCandidates {
		addMockToken(qs, walletID, tokenType, fmt.Sprintf("tx-%d", i), "10")
	}
	qs.WarmupCache(walletID, tokenType)

	_, metrics := setupMetricsMocks()

	picked := make(map[string]int)
	for range numTrials {
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(true, nil)
		s := sherdlock.NewSelector(sherdlock.Logger(), sherdlock.NewLazyFetcher(qs), mockLocker, testutils.TokenQuantityPrecision, metrics)

		tokens, _, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: walletID}, "10", tokenType)
		require.NoError(t, err)
		require.Len(t, tokens, 1)
		picked[tokens[0].TxId]++
	}

	assert.Greater(t, len(picked), 1, "expected the shuffle to spread selection across equal-amount candidates instead of always picking the same one, got: %v", picked)
}

// TestSizeOrderedSelection_SufficiencyWindowShuffle closes the gap left by
// TestSizeOrderedSelection_SmallestFit and TestSizeOrderedSelection_SameAmountBucketShuffle:
// neither covers several *distinct* amounts that each individually satisfy the request, which
// is exactly the #2395 CERT-incident shape ("a 1 EUR request grabbed the 200 EUR token")
// bucketedIterator's byte-equal-only shuffle (fetcher.go) cannot help with, since every bucket
// there has size 1 for distinct amounts. This test wires a wallet with amounts 2, 3, 4, 5, 6
// and 200 EUR - all individually sufficient for a 1 EUR request - and repeats the selection
// many times with a fresh Selector/locker each time (so there is no real lock contention, only
// nextCandidate's sufficiency-window randomization, selector.go) to assert:
//  1. the winning token is not always the same one (real statistical spread among the
//     individually-sufficient small candidates 2, 3, 4 and 5 EUR, the ones that fall within
//     the sufficiencyWindow=4 lookahead starting at the smallest sufficient candidate), and
//  2. the 6 and far-oversized 200 EUR tokens, both outside that lookahead window, are
//     essentially never chosen, preserving the smallest-fit bias the #2395 fix guidance also
//     required.
func TestSizeOrderedSelection_SufficiencyWindowShuffle(t *testing.T) {
	const walletID = "wallet-cert-incident"
	const tokenType = "EUR"
	const numTrials = 200

	qs := testutils.NewMockQueryService()
	addMockToken(qs, walletID, tokenType, "tx-2", "2")
	addMockToken(qs, walletID, tokenType, "tx-3", "3")
	addMockToken(qs, walletID, tokenType, "tx-4", "4")
	addMockToken(qs, walletID, tokenType, "tx-5", "5")
	addMockToken(qs, walletID, tokenType, "tx-6", "6")
	addMockToken(qs, walletID, tokenType, "tx-200", "200")
	qs.WarmupCache(walletID, tokenType)

	_, metrics := setupMetricsMocks()

	picked := make(map[string]int)
	for range numTrials {
		mockLocker := &mocks.FakeTokenLocker{}
		mockLocker.TryLockReturns(true, nil)
		s := sherdlock.NewSelector(sherdlock.Logger(), sherdlock.NewLazyFetcher(qs), mockLocker, testutils.TokenQuantityPrecision, metrics)

		tokens, sum, err := s.Select(t.Context(), &unitTestMockOwnerFilter{id: walletID}, "1", tokenType)
		require.NoError(t, err)
		require.Len(t, tokens, 1, "a 1 EUR request satisfiable by any single token must never need more than one")
		picked[tokens[0].TxId]++
		assert.NotEqual(t, "0", sum.Decimal())
	}

	assert.GreaterOrEqual(t, len(picked), 2,
		"expected real statistical spread among the distinct-amount sufficient candidates within the window (2/3/4/5 EUR), got: %v", picked)
	assert.Zero(t, picked["tx-200"],
		"expected the far-oversized 200 EUR token, outside the sufficiency window, to never be selected, got: %v", picked)
	assert.Zero(t, picked["tx-6"],
		"expected the 6 EUR token, outside the sufficiency window, to never be selected, got: %v", picked)
}

// addMockToken registers a token in qs under a key WarmupCache's substring filter can find:
// it must contain both walletID and tokenType (see MockQueryService.WarmupCache), mirroring
// the key shape used by benchmark_test.go's setup.
func addMockToken(qs *testutils.MockQueryService, walletID, tokenType, txID, quantity string) {
	owner := []byte(walletID)
	tok := &token2.UnspentToken{
		Id:       token2.ID{TxId: txID, Index: 0},
		Owner:    owner,
		Type:     token2.Type(tokenType),
		Quantity: quantity,
	}
	key := fmt.Sprintf("etoken.%s.%s.%s.%d", walletID, tokenType, txID, 0)
	qs.Add(key, tok)
}

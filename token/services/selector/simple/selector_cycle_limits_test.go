/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unspentTokens(n int, quantity string) []*token2.UnspentToken {
	tokens := make([]*token2.UnspentToken, n)
	for i := range n {
		tokens[i] = &token2.UnspentToken{
			Id:       token2.ID{TxId: "tx", Index: uint64(i)},
			Quantity: quantity,
		}
	}

	return tokens
}

// TestSelector_IterationLimitIsPerCycle pins that the token-iteration budget
// applies per retry cycle rather than cumulatively across a selection.
//
// The query is capped at maxTokensPerSelection rows, so a cumulative counter
// makes the effective per-cycle budget maxTokensPerSelection/maxRetries: a
// wallet well inside the limit still trips "exceeded max token iteration limit"
// on a later cycle, and that untyped error hides the real, typed reason the
// selection failed (contention) from callers that switch on the sentinels.
func TestSelector_IterationLimitIsPerCycle(t *testing.T) {
	// 30 tokens of 10 = 300 total, all of it requested. The locker grants the
	// first 15 locks and rejects the rest, so every cycle ends in "sufficient
	// funds but locked" and the selector retries. 30 tokens/cycle * 3 cycles =
	// 90 > maxTokensPerSelection, but no single cycle exceeds 50.
	locker := newMockLocker()
	locker.maxLockFail = 15

	s := &selector{
		txID:                  "test-tx",
		locker:                locker,
		queryService:          &mockQueryService{tokens: unspentTokens(30, "10")},
		precision:             64,
		maxRetries:            3,
		timeout:               time.Millisecond,
		maxTokensPerSelection: 50,
		maxLockAttempts:       1000,
		selectionTimeout:      10 * time.Second,
	}

	_, _, err := s.Select(context.Background(), &mockOwnerFilter{id: "alice"}, "300", "USD")
	require.Error(t, err)
	// 30 tokens are examined every cycle and the selection retries 3 times, so
	// the cumulative token count (90) is well past maxTokensPerSelection (50). A
	// cumulative counter would therefore trip the iteration guard on a later
	// cycle and mask the real, typed reason (contention). That the iteration
	// error never appears is the proof the budget is enforced per cycle.
	assert.NotContains(t, err.Error(), "exceeded max token iteration limit",
		"a wallet inside the per-cycle limit must not trip the iteration guard")
	assert.True(t, errors.HasCause(err, token.SelectorSufficientButLockedFunds),
		"expected SelectorSufficientButLockedFunds, got: %v", err)
}

// TestSelector_ShortPageFailsFastWithInsufficientFunds verifies the fast path:
// a cycle that reads fewer rows than the limit has seen the whole wallet, so
// the selection fails immediately with the typed sentinel instead of retrying.
func TestSelector_ShortPageFailsFastWithInsufficientFunds(t *testing.T) {
	locker := newMockLocker()
	s := &selector{
		txID:                  "test-tx",
		locker:                locker,
		queryService:          &mockQueryService{tokens: unspentTokens(5, "10")},
		precision:             64,
		maxRetries:            3,
		timeout:               time.Millisecond,
		maxTokensPerSelection: 50,
		maxLockAttempts:       1000,
		selectionTimeout:      10 * time.Second,
	}

	_, _, err := s.Select(context.Background(), &mockOwnerFilter{id: "alice"}, "1000", "USD")
	require.Error(t, err)
	assert.True(t, errors.HasCause(err, token.SelectorInsufficientFunds),
		"expected SelectorInsufficientFunds, got: %v", err)
	// The whole 5-token wallet fits in one page (fewer rows than the cap+1
	// fetched), so the selection sees every token in a single cycle and fails
	// immediately: exactly 5 lock attempts, no retry.
	assert.Equal(t, 5, locker.lockCount, "must not retry once the whole wallet was read")
}

// TestSelector_FullPageReportsIterationLimit verifies the other side of the
// split: when a cycle fills its page and the funds are still short, the wallet
// holds more tokens than the selection may examine. The query ordering is
// deterministic, so retrying re-reads the same page — the selector reports the
// limit rather than spinning until the timeout.
func TestSelector_FullPageReportsIterationLimit(t *testing.T) {
	locker := newMockLocker()
	s := &selector{
		txID:                  "test-tx",
		locker:                locker,
		queryService:          &mockQueryService{tokens: unspentTokens(100, "10")},
		precision:             64,
		maxRetries:            3,
		timeout:               time.Millisecond,
		maxTokensPerSelection: 50,
		maxLockAttempts:       1000,
		selectionTimeout:      10 * time.Second,
	}

	_, _, err := s.Select(context.Background(), &mockOwnerFilter{id: "alice"}, "1000", "USD")
	require.Error(t, err)
	assert.True(t, errors.HasCause(err, token.SelectorResourceLimitExceeded),
		"expected SelectorResourceLimitExceeded, got: %v", err)
	assert.Contains(t, err.Error(), "exceeded max token iteration limit")
	assert.Contains(t, err.Error(), "50 tokens")
	// The wallet holds more tokens than a page (100 > cap of 50), so the (cap+1)th
	// row proves the page is truncated and the guard fires on the very first
	// cycle. It locks the 50 tokens it examined, so lockCount stays at 50: the
	// limit is reported on the first full page, not after retrying.
	assert.Equal(t, 50, locker.lockCount, "must report on the first full page, not after retrying")
}

// TestSelector_ZeroSelectionTimeoutMeansNoTimeout guards the zero value of
// selectionTimeout. context.WithTimeout(ctx, 0) is already expired, so an
// omitted timeout would make every Select fail after examining zero tokens.
func TestSelector_ZeroSelectionTimeoutMeansNoTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		s := &selector{
			txID:                  "test-tx",
			locker:                newMockLocker(),
			queryService:          &mockQueryService{tokens: unspentTokens(5, "10")},
			precision:             64,
			maxRetries:            3,
			timeout:               time.Millisecond,
			maxTokensPerSelection: 50,
			maxLockAttempts:       1000,
			selectionTimeout:      timeout,
		}

		ids, sum, err := s.Select(context.Background(), &mockOwnerFilter{id: "alice"}, "30", "USD")
		require.NoError(t, err, "selectionTimeout %v must mean no timeout", timeout)
		assert.Equal(t, "30", sum.Decimal())
		assert.Len(t, ids, 3)
	}
}

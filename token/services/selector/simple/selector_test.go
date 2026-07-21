/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

const precision = uint64(64)

// ownerFilter is a minimal token.OwnerFilter.
type ownerFilter struct{ id string }

func (o *ownerFilter) ID() string                                { return o.id }
func (o *ownerFilter) ContainsToken(_ *token2.UnspentToken) bool { return true }

// sliceIterator walks a fixed slice of UnspentTokens.
type sliceIterator struct {
	tokens []*token2.UnspentToken
	pos    int
}

func (s *sliceIterator) Close() {}
func (s *sliceIterator) Next() (*token2.UnspentToken, error) {
	if s.pos >= len(s.tokens) {
		return nil, nil
	}
	t := s.tokens[s.pos]
	s.pos++

	return t, nil
}

// mockQueryService returns the iterator and optionally fails GetTokens.
type mockQueryService struct {
	tokens         []*token2.UnspentToken
	getTokensError error
}

func (m *mockQueryService) UnspentTokensIterator(_ context.Context) (*token.UnspentTokensIterator, error) {
	panic("not used")
}

func (m *mockQueryService) UnspentTokensIteratorBy(_ context.Context, _ string, _ token2.Type) (driver.UnspentTokensIterator, error) {
	return &sliceIterator{tokens: m.tokens}, nil
}

func (m *mockQueryService) GetTokens(_ context.Context, _ ...*token2.ID) ([]*token2.Token, error) {
	if m.getTokensError != nil {
		return nil, m.getTokensError
	}

	return nil, nil
}

// recordingLocker records every call to Lock and UnlockIDs.
type recordingLocker struct {
	// lockErr is returned for the lockFailAfter-th call onwards (0-indexed).
	lockFailAfter int // after this many successes, start returning lockErr
	lockErr       error
	calls         int // total Lock calls

	unlocked [][]*token2.ID // each UnlockIDs call appended as a group
}

func (r *recordingLocker) Lock(_ context.Context, id *token2.ID, _ string, _ string, _ bool) (string, error) {
	idx := r.calls
	r.calls++
	if idx >= r.lockFailAfter {
		return "", r.lockErr
	}

	return "locked", nil
}

func (r *recordingLocker) UnlockIDs(_ context.Context, ids ...*token2.ID) []*token2.ID {
	if len(ids) > 0 {
		cp := make([]*token2.ID, len(ids))
		copy(cp, ids)
		r.unlocked = append(r.unlocked, cp)
	}

	return nil
}

func (r *recordingLocker) UnlockByTxID(_ context.Context, _ string) {}
func (r *recordingLocker) IsLocked(_ *token2.ID) bool               { return false }

// totalUnlocked returns the flat list of all IDs passed to any UnlockIDs call.
func (r *recordingLocker) totalUnlocked() []*token2.ID {
	var out []*token2.ID
	for _, group := range r.unlocked {
		out = append(out, group...)
	}

	return out
}

// makeTokens builds n tokens, each with quantity "0x1" (= 1 in hex) and
// the given type. One token at index badQuantityAt (0-based) is given an
// unparseable quantity string; pass -1 to skip.
func makeTokens(n int, typ token2.Type, badQuantityAt int) []*token2.UnspentToken {
	tokens := make([]*token2.UnspentToken, n)
	for i := range n {
		q := "0x1"
		if i == badQuantityAt {
			q = "NOT_A_NUMBER"
		}
		tokens[i] = &token2.UnspentToken{
			Id:       token2.ID{TxId: fmt.Sprintf("tx%d", i), Index: 0},
			Owner:    []byte("wallet1"),
			Type:     typ,
			Quantity: q,
		}
	}

	return tokens
}

// newSelector is a convenience constructor.
func newSelector(locker Locker, qs QueryService, numRetry int) *selector {
	return &selector{
		txID:         "testTx",
		locker:       locker,
		queryService: qs,
		precision:    precision,
		numRetry:     numRetry,
		timeout:      0,
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestSelectByID_ToQuantityError: the second token (index 1) has an unparseable
// quantity. Token 0 is locked successfully before the failure is hit and must
// be unlocked. We ask for 3 tokens worth of value so the loop is not broken
// early by hitting the target.
func TestSelectByID_ToQuantityError(t *testing.T) {
	// token 0: valid "0x1", token 1: bad, token 2: valid "0x1"
	// target = 0x3 → the loop will try all three before summing enough; hits bad token at index 1
	tokens := []*token2.UnspentToken{
		{Id: token2.ID{TxId: "tx0", Index: 0}, Owner: []byte("wallet1"), Type: "USD", Quantity: "0x1"},
		{Id: token2.ID{TxId: "tx1", Index: 0}, Owner: []byte("wallet1"), Type: "USD", Quantity: "NOT_A_NUMBER"},
		{Id: token2.ID{TxId: "tx2", Index: 0}, Owner: []byte("wallet1"), Type: "USD", Quantity: "0x1"},
	}

	locker := &recordingLocker{lockFailAfter: 10} // all locks succeed
	qs := &mockQueryService{tokens: tokens}
	sel := newSelector(locker, qs, 1)

	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "wallet1"}, "0x3", "USD")
	require.Error(t, err, "expected an error from bad quantity")

	// token 0 was locked and must have been unlocked
	unlocked := locker.totalUnlocked()
	require.Len(t, unlocked, 1, "the one successfully-locked token must be unlocked")
}

// TestSelectByID_RateLimited: tokens 0 & 1 are locked successfully, then token 2
// causes the Locker to return an error wrapping token.SelectorRateLimited.
// The already-locked tokens must be unlocked, and the error must be returned
// directly without any retry — this is the contract an application-supplied,
// wallet-id-aware Locker uses to integrate its own rate limiting.
func TestSelectByID_RateLimited(t *testing.T) {
	locker := &recordingLocker{
		lockFailAfter: 2, // tokens 0 & 1 succeed, token 2 fails
		lockErr:       errors.Wrapf(token.SelectorRateLimited, "wallet wallet1 throttled"),
	}
	qs := &mockQueryService{tokens: makeTokens(4, "USD", -1)}
	sel := newSelector(locker, qs, 5) // 5 retries — must NOT retry on rate-limit error

	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "wallet1"}, "0x4", "USD")
	require.ErrorIs(t, err, token.SelectorRateLimited)

	// tokens 0 & 1 were locked and must be unlocked
	unlocked := locker.totalUnlocked()
	require.Len(t, unlocked, 2, "both successfully-locked tokens must be unlocked")

	assert.Equal(t, 3, locker.calls, "should not retry after rate limit exceeded")
}

// TestSelectByID_ConcurrencyCheckFailure: all tokens lock fine and sum is
// sufficient, but GetTokens (the concurrency check) returns an error.
// All locked tokens must be unlocked and the loop must retry.
func TestSelectByID_ConcurrencyCheckFailure(t *testing.T) {
	locker := &recordingLocker{lockFailAfter: 100} // all locks succeed
	qs := &mockQueryService{
		tokens:         makeTokens(2, "USD", -1),
		getTokensError: errors.New("token no longer exists"),
	}
	// numRetry=1 means a single attempt then fail with SelectorSufficientFundsButConcurrencyIssue
	sel := newSelector(locker, qs, 1)

	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "wallet1"}, "0x2", "USD")
	require.ErrorIs(t, err, token.SelectorSufficientFundsButConcurrencyIssue)

	// both tokens were locked and must have been unlocked on the retry/failure path
	unlocked := locker.totalUnlocked()
	require.Len(t, unlocked, 2, "all locked tokens must be unlocked after concurrency failure")
}

// TestSelectByID_InsufficientFunds: only 1 token available but 2 requested.
// The single token gets locked, then unlocked at the end of each retry,
// and the final error must be SelectorInsufficientFunds.
func TestSelectByID_InsufficientFunds(t *testing.T) {
	locker := &recordingLocker{lockFailAfter: 100}
	qs := &mockQueryService{tokens: makeTokens(1, "USD", -1)}
	sel := newSelector(locker, qs, 2) // 2 retries

	_, _, err := sel.Select(context.Background(), &ownerFilter{id: "wallet1"}, "0x2", "USD")
	require.ErrorIs(t, err, token.SelectorInsufficientFunds)

	// The token must have been unlocked once per retry attempt (2 retries × 1 token)
	assert.Len(t, locker.unlocked, 2, "token must be unlocked after each retry")
}

// TestSelectByID_HappyPath: enough tokens exist and locking succeeds.
// No UnlockIDs should be called.
func TestSelectByID_HappyPath(t *testing.T) {
	locker := &recordingLocker{lockFailAfter: 100}
	qs := &mockQueryService{tokens: makeTokens(3, "USD", -1)}
	sel := newSelector(locker, qs, 1)

	ids, sum, err := sel.Select(context.Background(), &ownerFilter{id: "wallet1"}, "0x2", "USD")
	require.NoError(t, err)
	require.Len(t, ids, 2)
	assert.Equal(t, 0, sum.Cmp(token2.NewQuantityFromUInt64(2)), "sum should be 2")

	// no unlocks should have happened
	assert.Empty(t, locker.unlocked, "no tokens should be unlocked on success")
}

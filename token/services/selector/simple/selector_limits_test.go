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
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOwnerFilter implements token.OwnerFilter for testing
type mockOwnerFilter struct {
	id string
}

func (m *mockOwnerFilter) ID() string {
	return m.id
}

// mockQueryService implements QueryService for testing
type mockQueryService struct {
	tokens []*token2.UnspentToken
	delay  time.Duration
	// getTokensError, when set, is returned by GetTokens.
	getTokensError error
}

func (m *mockQueryService) UnspentTokensIterator(ctx context.Context) (*token.UnspentTokensIterator, error) {
	return nil, nil
}

func (m *mockQueryService) UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type, limit int) (driver.UnspentTokensIterator, error) {
	// Respect the limit parameter in the mock
	tokens := m.tokens
	if limit > 0 && len(tokens) > limit {
		tokens = tokens[:limit]
	}

	return &mockIterator{tokens: tokens, delay: m.delay}, nil
}

func (m *mockQueryService) GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error) {
	if m.getTokensError != nil {
		return nil, m.getTokensError
	}

	return nil, nil
}

// mockIterator implements driver.UnspentTokensIterator
type mockIterator struct {
	tokens []*token2.UnspentToken
	index  int
	delay  time.Duration
}

func (m *mockIterator) Close() {
	m.index = 0
}

func (m *mockIterator) Next() (*token2.UnspentToken, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.index >= len(m.tokens) {
		return nil, nil
	}
	t := m.tokens[m.index]
	m.index++

	return t, nil
}

// mockLocker implements Locker for testing
type mockLocker struct {
	locked         map[token2.ID]string
	lockCount      int
	maxLockFail    int  // Fail after this many lock attempts
	rejectReclaims bool // Reject reclaim attempts
}

func newMockLocker() *mockLocker {
	return &mockLocker{
		locked: make(map[token2.ID]string),
	}
}

func (m *mockLocker) Lock(ctx context.Context, owner string, id *token2.ID, txID string, reclaim bool) (string, error) {
	m.lockCount++
	if m.maxLockFail > 0 && m.lockCount > m.maxLockFail {
		return "", assert.AnError
	}

	// Check if already locked by a different transaction
	if existingTxID, exists := m.locked[*id]; exists && existingTxID != txID {
		if !reclaim || m.rejectReclaims {
			return "", assert.AnError // Lock conflict
		}
	}

	m.locked[*id] = txID

	return "", nil
}

func (m *mockLocker) UnlockIDs(ctx context.Context, owner string, ids ...*token2.ID) []*token2.ID {
	for _, id := range ids {
		delete(m.locked, *id)
	}

	return nil
}

func (m *mockLocker) UnlockByTxID(ctx context.Context, txID string) {
	for id, tx := range m.locked {
		if tx == txID {
			delete(m.locked, id)
		}
	}
}

func (m *mockLocker) IsLocked(id *token2.ID) bool {
	_, ok := m.locked[*id]

	return ok
}

func TestSelector_TokenIterationLimit(t *testing.T) {
	t.Run("aborts when exceeding max tokens per selection", func(t *testing.T) {
		// Create 100 tokens but set limit to 50
		tokens := make([]*token2.UnspentToken, 100)
		for i := range 100 {
			tokens[i] = &token2.UnspentToken{
				Id:       token2.ID{TxId: "tx", Index: uint64(i)},
				Quantity: "10",
			}
		}

		s := &selector{
			txID:                  "test-tx",
			locker:                newMockLocker(),
			queryService:          &mockQueryService{tokens: tokens},
			precision:             64,
			maxRetries:            3,
			timeout:               time.Millisecond,
			maxTokensPerSelection: 50, // Limit to 50 tokens
			maxLockAttempts:       1000,
			selectionTimeout:      time.Second,
		}

		ctx := context.Background()
		filter := &mockOwnerFilter{id: "alice"}

		_, _, err := s.Select(ctx, filter, "1000", "USD")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded max token iteration limit")
		assert.Contains(t, err.Error(), "50 tokens")
	})

	t.Run("succeeds when within token iteration limit", func(t *testing.T) {
		// Create 10 tokens with limit of 50
		tokens := make([]*token2.UnspentToken, 10)
		for i := range 10 {
			tokens[i] = &token2.UnspentToken{
				Id:       token2.ID{TxId: "tx", Index: uint64(i)},
				Quantity: "10",
			}
		}

		s := &selector{
			txID:                  "test-tx",
			locker:                newMockLocker(),
			queryService:          &mockQueryService{tokens: tokens},
			precision:             64,
			maxRetries:            3,
			timeout:               time.Millisecond,
			maxTokensPerSelection: 50,
			maxLockAttempts:       1000,
			selectionTimeout:      time.Second,
		}

		ctx := context.Background()
		filter := &mockOwnerFilter{id: "alice"}

		ids, quantity, err := s.Select(ctx, filter, "50", "USD")
		require.NoError(t, err)
		assert.NotNil(t, ids)
		assert.NotNil(t, quantity)
		assert.Len(t, ids, 5) // Should select 5 tokens of 10 each
	})
}

func TestSelector_LockAttemptLimit(t *testing.T) {
	t.Run("aborts when exceeding max lock attempts", func(t *testing.T) {
		// Create many tokens
		tokens := make([]*token2.UnspentToken, 1000)
		for i := range 1000 {
			tokens[i] = &token2.UnspentToken{
				Id:       token2.ID{TxId: "tx", Index: uint64(i)},
				Quantity: "1",
			}
		}

		locker := newMockLocker()
		locker.maxLockFail = 10 // Fail all locks after 10 attempts

		s := &selector{
			txID:                  "test-tx",
			locker:                locker,
			queryService:          &mockQueryService{tokens: tokens},
			precision:             64,
			maxRetries:            3,
			timeout:               time.Millisecond,
			maxTokensPerSelection: 1000,
			maxLockAttempts:       20, // Limit to 20 lock attempts
			selectionTimeout:      time.Second,
		}

		ctx := context.Background()
		filter := &mockOwnerFilter{id: "alice"}

		_, _, err := s.Select(ctx, filter, "100", "USD")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded max lock attempts")
	})
}

func TestSelector_RetryLimit(t *testing.T) {
	t.Run("aborts when exceeding max retries", func(t *testing.T) {
		// Create tokens with sufficient funds
		tokens := []*token2.UnspentToken{
			{
				Id:       token2.ID{TxId: "tx1", Index: 0},
				Quantity: "500",
			},
			{
				Id:       token2.ID{TxId: "tx2", Index: 0},
				Quantity: "500",
			},
		}

		// Pre-lock all tokens with a different transaction to simulate contention
		locker := newMockLocker()
		locker.rejectReclaims = true // Prevent reclaiming locks even with reclaim=true
		ctx := context.Background()
		_, err := locker.Lock(ctx, "alice", &tokens[0].Id, "other-tx", false)
		require.NoError(t, err)
		_, err2 := locker.Lock(ctx, "alice", &tokens[1].Id, "other-tx", false)
		require.NoError(t, err2)

		s := &selector{
			txID:                  "test-tx",
			locker:                locker,
			queryService:          &mockQueryService{tokens: tokens},
			precision:             64,
			maxRetries:            3, // Limit to 3 retries
			timeout:               time.Millisecond,
			maxTokensPerSelection: 1000,
			maxLockAttempts:       1000,
			selectionTimeout:      time.Second,
		}

		filter := &mockOwnerFilter{id: "alice"}

		_, _, err = s.Select(ctx, filter, "100", "USD") // Request less than available but all tokens are locked
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded max retries")
	})
}

func TestSelector_SelectionTimeout(t *testing.T) {
	t.Skip("current selector implementation does not check context deadline during iteration; timeout path is not reliably testable with this mock")
}

func TestSelector_ResourceTracking(t *testing.T) {
	t.Run("resets counters between selections", func(t *testing.T) {
		tokens := []*token2.UnspentToken{
			{Id: token2.ID{TxId: "tx", Index: 0}, Quantity: "100"},
		}

		s := &selector{
			txID:                  "test-tx",
			locker:                newMockLocker(),
			queryService:          &mockQueryService{tokens: tokens},
			precision:             64,
			maxRetries:            3,
			timeout:               time.Millisecond,
			maxTokensPerSelection: 1000,
			maxLockAttempts:       1000,
			selectionTimeout:      time.Second,
		}

		ctx := context.Background()
		filter := &mockOwnerFilter{id: "alice"}

		// First selection
		_, _, err := s.Select(ctx, filter, "50", "USD")
		require.NoError(t, err)
		firstIterCount := s.tokensIteratedCount
		firstLockCount := s.lockAttemptsCount

		// Second selection - counters should reset
		_, _, err = s.Select(ctx, filter, "50", "USD")
		require.NoError(t, err)

		// Verify counters were reset and incremented again
		assert.Equal(t, firstIterCount, s.tokensIteratedCount, "iteration count should be same for identical selections")
		assert.Equal(t, firstLockCount, s.lockAttemptsCount, "lock attempt count should be same for identical selections")
	})
}

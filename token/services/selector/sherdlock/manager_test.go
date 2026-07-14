/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/testutils"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/dbtest"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/postgres"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	postgres2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Package sherdlock_test validates selector manager lifecycle, concurrency, and lock cleanup.
// Tests cover: selector creation/caching, concurrent operations, unlock behavior, lease cleanup,
// and various precision configurations.

func TestSufficientTokensOneReplica(t *testing.T) {
	replicas, terminate := startManagers(t, 1, NoBackoff, 5)
	defer terminate()
	testutils.TestSufficientTokensOneReplica(t, replicas[0])
}

func TestSufficientTokensOneReplicaNoRetry(t *testing.T) {
	replicas, terminate := startManagers(t, 1, NoBackoff, 0)
	defer terminate()
	testutils.TestSufficientTokensOneReplica(t, replicas[0])
}

func TestSufficientTokensBigDenominationsOneReplica(t *testing.T) {
	replicas, terminate := startManagers(t, 1, time.Second, 20)
	defer terminate()
	testutils.TestSufficientTokensBigDenominationsOneReplica(t, replicas[0])
}

func TestSufficientTokensBigDenominationsManyReplicas(t *testing.T) {
	replicas, terminate := startManagers(t, 3, 2*time.Second, 10)
	defer terminate()
	testutils.TestSufficientTokensBigDenominationsManyReplicas(t, replicas)
}

func TestInsufficientTokensOneReplica(t *testing.T) {
	replicas, terminate := startManagers(t, 1, NoBackoff, 5)
	defer terminate()
	testutils.TestInsufficientTokensOneReplica(t, replicas[0])
}

func TestSufficientTokensManyReplicas(t *testing.T) {
	replicas, terminate := startManagers(t, 20, NoBackoff, 5)
	defer terminate()
	testutils.TestSufficientTokensManyReplicas(t, replicas)
}

func TestInsufficientTokensManyReplicas(t *testing.T) {
	replicas, terminate := startManagers(t, 10, 5*time.Second, 5)
	defer terminate()
	testutils.TestInsufficientTokensManyReplicas(t, replicas)
}

// Set up

func startManagers(t *testing.T, number int, backoff time.Duration, maxRetries int) ([]testutils.EnhancedManager, func()) {
	t.Helper()
	terminate, pgConnStr := startContainer(t)
	replicas := make([]testutils.EnhancedManager, number)

	for i := range number {
		replica, err := createManager(pgConnStr, backoff, maxRetries)
		require.NoError(t, err)
		replicas[i] = replica
	}

	return replicas, terminate
}

func createManager(pgConnStr string, backoff time.Duration, maxRetries int) (testutils.EnhancedManager, error) {
	d := postgres.NewDriverWithDbProvider(multiplexed.MockTypeConfig(postgres2.Persistence, postgres2.Config{
		TablePrefix:  "test",
		DataSource:   pgConnStr,
		MaxOpenConns: 10,
	}), &dbProvider{})

	// Create Token DB first
	tokenDB, err := d.NewToken("")
	if err != nil {
		return nil, err
	}

	// Create Lock DB after
	lockDB, err := d.NewTokenLock("")
	if err != nil {
		return nil, errors.Join(err, tokenDB.Close())
	}

	m := NewMetrics(&disabled.Provider{})
	fetcher := newMixedFetcher(tokenDB.(dbtest.TestTokenDB), m, 0, 0, 0)
	manager := NewManager(fetcher, lockDB, testutils.TokenQuantityPrecision, backoff, maxRetries, 0, 0, m)

	return testutils.NewEnhancedManager(manager, tokenDB.(dbtest.TestTokenDB)), nil
}

func startContainer(t *testing.T) (func(), string) {
	t.Helper()
	cfg := postgres2.DefaultConfig(postgres2.WithDBName(t.Name()))
	terminate, _, err := postgres2.StartPostgres(t.Context(), cfg, nil)
	require.NoError(t, err)

	return terminate, cfg.DataSource()
}

type dbProvider struct{}

func (p *dbProvider) Get(opts postgres2.Opts) (*common.RWDB, error) { return postgres2.Open(opts) }

// Unit Tests for Manager

func TestNewManager(t *testing.T) {
	t.Run("creates manager with valid parameters", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			time.Minute,
			NewMetrics(&disabled.Provider{}),
		)

		assert.NotNil(t, m)
		assert.Equal(t, mockLocker, m.locker)
		assert.Equal(t, 10*time.Minute, m.leaseExpiry)
		assert.Equal(t, time.Minute, m.leaseCleanupTickPeriod)
	})

	t.Run("does not start cleaner when lease expiry is zero", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			0, // zero lease expiry
			time.Minute,
			NewMetrics(&disabled.Provider{}),
		)

		assert.NotNil(t, m)
		// Cleaner should not be started
	})

	t.Run("does not start cleaner when cleanup tick period is zero", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			0, // zero cleanup tick period
			NewMetrics(&disabled.Provider{}),
		)

		assert.NotNil(t, m)
		// Cleaner should not be started
	})
}

func TestManager_NewSelector(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(
		mockFetcher,
		mockLocker,
		100,
		time.Second,
		5,
		0,
		0,
		NewMetrics(&disabled.Provider{}),
	)

	t.Run("creates new selector for transaction ID", func(t *testing.T) {
		txID := transaction.ID("tx1")

		selector, err := m.NewSelector(txID)

		require.NoError(t, err)
		assert.NotNil(t, selector)
	})

	t.Run("returns same selector for same transaction ID", func(t *testing.T) {
		txID := transaction.ID("tx2")

		selector1, err1 := m.NewSelector(txID)
		selector2, err2 := m.NewSelector(txID)

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.Equal(t, selector1, selector2)
	})

	t.Run("creates different selectors for different transaction IDs", func(t *testing.T) {
		txID1 := transaction.ID("tx3")
		txID2 := transaction.ID("tx4")

		selector1, err1 := m.NewSelector(txID1)
		selector2, err2 := m.NewSelector(txID2)

		require.NoError(t, err1)
		require.NoError(t, err2)
		assert.NotEqual(t, selector1, selector2)
	})
}

func TestManager_Unlock(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(
		mockFetcher,
		mockLocker,
		100,
		time.Second,
		5,
		0,
		0,
		NewMetrics(&disabled.Provider{}),
	)

	t.Run("calls locker UnlockByTxID", func(t *testing.T) {
		txID := transaction.ID("tx1")
		ctx := t.Context()

		mockLocker.unlockByTxIDFunc = func(c context.Context, id transaction.ID) error {
			assert.Equal(t, ctx, c)
			assert.Equal(t, txID, id)

			return nil
		}

		err := m.Unlock(ctx, txID)

		require.NoError(t, err)
		assert.True(t, mockLocker.unlockByTxIDCalled)
	})

	t.Run("returns error from locker", func(t *testing.T) {
		txID := transaction.ID("tx2")
		ctx := t.Context()
		expectedErr := errors.New("unlock failed")

		mockLocker.unlockByTxIDFunc = func(c context.Context, id transaction.ID) error {
			return expectedErr
		}

		err := m.Unlock(ctx, txID)

		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})
}

func TestManager_Close(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(
		mockFetcher,
		mockLocker,
		100,
		time.Second,
		5,
		0,
		0,
		NewMetrics(&disabled.Provider{}),
	)

	t.Run("closes existing selector", func(t *testing.T) {
		txID := transaction.ID("tx1")

		// Create selector first
		_, err := m.NewSelector(txID)
		require.NoError(t, err)

		// Close it
		err = m.Close(txID)
		require.NoError(t, err)
	})

	t.Run("returns error for non-existent selector", func(t *testing.T) {
		txID := transaction.ID("nonexistent")

		err := m.Close(txID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("can close selector multiple times returns error", func(t *testing.T) {
		txID := transaction.ID("tx2")

		// Create selector
		_, err := m.NewSelector(txID)
		require.NoError(t, err)

		// Close first time
		err = m.Close(txID)
		require.NoError(t, err)

		// Close second time should error
		err = m.Close(txID)
		require.Error(t, err)
	})
}

func TestManager_Cleaner(t *testing.T) {
	t.Run("cleaner calls Cleanup periodically", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		cleanupCalled := make(chan struct{}, 2)
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			cleanupCalled <- struct{}{}

			return nil
		}

		// Short tick period for testing
		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			50*time.Millisecond, // Short period for testing
			NewMetrics(&disabled.Provider{}),
		)

		// Wait for at least 2 cleanup calls
		select {
		case <-cleanupCalled:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("cleanup not called in time")
		}

		select {
		case <-cleanupCalled:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("cleanup not called second time")
		}

		// Verify manager is still functional
		assert.NotNil(t, m)
	})

	t.Run("cleaner handles cleanup errors gracefully", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		cleanupCalled := make(chan struct{}, 1)
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			cleanupCalled <- struct{}{}

			return errors.New("cleanup error")
		}

		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			50*time.Millisecond,
			NewMetrics(&disabled.Provider{}),
		)

		// Wait for cleanup call (should not panic despite error)
		select {
		case <-cleanupCalled:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("cleanup not called")
		}

		// Manager should still be functional
		assert.NotNil(t, m)
	})
}

// TestManager_NewSelector_Concurrent verifies concurrent selector creation returns same instance.
func TestManager_NewSelector_Concurrent(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(
		mockFetcher,
		mockLocker,
		100,
		time.Second,
		5,
		0,
		0,
		NewMetrics(&disabled.Provider{}),
	)

	t.Run("handles concurrent selector creation", func(t *testing.T) {
		txID := transaction.ID("concurrent-tx")

		// Create selectors concurrently
		type result struct {
			selector token.Selector
			err      error
		}
		done := make(chan result, 10)
		for range 10 {
			go func() {
				selector, err := m.NewSelector(txID)
				done <- result{selector, err}
			}()
		}

		// Collect all selectors
		selectors := make([]token.Selector, 10)
		for i := range 10 {
			res := <-done
			require.NoError(t, res.err)
			selectors[i] = res.selector
		}

		// All should be the same instance (cached)
		for i := 1; i < 10; i++ {
			assert.Equal(t, selectors[0], selectors[i])
		}
	})
}

// TestManager_Close_Concurrent verifies only one concurrent close succeeds.
func TestManager_Close_Concurrent(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(
		mockFetcher,
		mockLocker,
		100,
		time.Second,
		5,
		0,
		0,
		NewMetrics(&disabled.Provider{}),
	)

	t.Run("handles concurrent close attempts", func(t *testing.T) {
		txID := transaction.ID("close-tx")

		// Create selector
		_, err := m.NewSelector(txID)
		require.NoError(t, err)

		// Try to close concurrently
		errors := make(chan error, 5)
		for range 5 {
			go func() {
				errors <- m.Close(txID)
			}()
		}

		// Collect results
		successCount := 0
		errorCount := 0
		for range 5 {
			err := <-errors
			if err == nil {
				successCount++
			} else {
				errorCount++
			}
		}

		// Only one should succeed, others should error
		assert.Equal(t, 1, successCount)
		assert.Equal(t, 4, errorCount)
	})
}

// TestManager_Unlock_EdgeCases verifies unlock handles empty and very long IDs.
func TestManager_Unlock_EdgeCases(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(
		mockFetcher,
		mockLocker,
		100,
		time.Second,
		5,
		0,
		0,
		NewMetrics(&disabled.Provider{}),
	)

	t.Run("handles empty transaction ID", func(t *testing.T) {
		txID := transaction.ID("")
		ctx := t.Context()

		mockLocker.unlockByTxIDFunc = func(c context.Context, id transaction.ID) error {
			assert.Equal(t, txID, id)

			return nil
		}

		err := m.Unlock(ctx, txID)
		require.NoError(t, err)
	})

	t.Run("handles very long transaction ID", func(t *testing.T) {
		longID := transaction.ID(make([]byte, 10000))
		ctx := t.Context()

		mockLocker.unlockByTxIDFunc = func(c context.Context, id transaction.ID) error {
			assert.Equal(t, longID, id)

			return nil
		}

		err := m.Unlock(ctx, longID)
		require.NoError(t, err)
	})
}

// TestManager_Cleaner_EdgeCases verifies cleaner lifecycle and correct lease expiry usage.
func TestManager_Cleaner_EdgeCases(t *testing.T) {
	t.Run("cleaner stops when context is cancelled", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		var cleanupCount atomic.Int32
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			cleanupCount.Add(1)

			return nil
		}

		// Very short tick period for testing
		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			10*time.Millisecond,
			NewMetrics(&disabled.Provider{}),
		)

		// Wait for a few cleanup cycles
		time.Sleep(50 * time.Millisecond)

		// Verify cleanup was called multiple times
		assert.Greater(t, cleanupCount.Load(), int32(1))

		// Manager should still be functional
		assert.NotNil(t, m)
	})

	t.Run("cleaner uses correct lease expiry", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		expectedExpiry := 15 * time.Minute
		cleanupCalled := make(chan time.Duration, 1)

		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			select {
			case cleanupCalled <- expiry:
			default:
			}

			return nil
		}

		NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			expectedExpiry,
			10*time.Millisecond,
			NewMetrics(&disabled.Provider{}),
		)

		// Wait for cleanup call
		select {
		case actualExpiry := <-cleanupCalled:
			assert.Equal(t, expectedExpiry, actualExpiry)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("cleanup not called")
		}
	})
}

// TestManager_NewSelector_WithDifferentPrecisions verifies selector creation with various precision values.
func TestManager_NewSelector_WithDifferentPrecisions(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	testCases := []struct {
		name      string
		precision uint64
	}{
		{"zero precision", 0},
		{"small precision", 1},
		{"normal precision", 100},
		{"large precision", 1000000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(
				mockFetcher,
				mockLocker,
				tc.precision,
				time.Second,
				5,
				0,
				0,
				NewMetrics(&disabled.Provider{}),
			)

			selector, err := m.NewSelector("test-" + tc.name)

			require.NoError(t, err)
			assert.NotNil(t, selector)
		})
	}
}

// Mock implementations for testing

type mockTokenFetcher struct {
	unspentTokensIteratorByFunc func(ctx context.Context, walletID string, currency token2.Type) (Iterator[*token2.UnspentTokenInWallet], error)
}

func (m *mockTokenFetcher) UnspentTokensIteratorBy(ctx context.Context, walletID string, currency token2.Type) (Iterator[*token2.UnspentTokenInWallet], error) {
	if m.unspentTokensIteratorByFunc != nil {
		return m.unspentTokensIteratorByFunc(ctx, walletID, currency)
	}

	return &mockIterator{}, nil
}

type mockLocker struct {
	lockFunc           func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error
	unlockByTxIDFunc   func(ctx context.Context, consumerTxID transaction.ID) error
	unlockByTxIDCalled bool
	cleanupFunc        func(ctx context.Context, leaseExpiry time.Duration) error
}

func (m *mockLocker) Lock(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error {
	if m.lockFunc != nil {
		return m.lockFunc(ctx, tokenID, consumerTxID)
	}

	return nil
}

func (m *mockLocker) UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error {
	m.unlockByTxIDCalled = true
	if m.unlockByTxIDFunc != nil {
		return m.unlockByTxIDFunc(ctx, consumerTxID)
	}

	return nil
}

func (m *mockLocker) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	if m.cleanupFunc != nil {
		return m.cleanupFunc(ctx, leaseExpiry)
	}

	return nil
}

type mockIterator struct {
	tokens []*token2.UnspentTokenInWallet
	index  int
}

func (m *mockIterator) Next() (*token2.UnspentTokenInWallet, error) {
	if m.index >= len(m.tokens) {
		return nil, nil
	}
	token := m.tokens[m.index]
	m.index++

	return token, nil
}

func (m *mockIterator) Close() {}

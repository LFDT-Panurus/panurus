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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
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
	// The retry budget has to absorb contention, not decide the result. 300
	// goroutines (3 replicas x 100 requests) contend for only 2 tokens, so the
	// herd drains slowly and unlucky goroutines retry before they win a lock.
	// The budget can stay at the pre-existing 10 as long as each selection
	// releases its partial locks before backing off; if that release is dropped,
	// goroutines sit on each other's funds and this test starts reporting
	// "insufficient funds" with funds available. See #2121.
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
		replica, err := createManager(t, pgConnStr, backoff, maxRetries)
		require.NoError(t, err)
		replicas[i] = replica
	}

	return replicas, terminate
}

func createManager(t *testing.T, pgConnStr string, backoff time.Duration, maxRetries int) (testutils.EnhancedManager, error) {
	t.Helper()
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
	manager := NewManager(&Config{
		Fetcher:                fetcher,
		Locker:                 lockDB,
		Precision:              testutils.TokenQuantityPrecision,
		Backoff:                backoff,
		MaxRetriesAfterBackOff: maxRetries,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		// Keep the outer retry-cycle cap consistent with the per-test
		// backoff-retry budget so it doesn't silently bind before it (the
		// previous hardcoded 10 capped tests that requested more retries).
		// Generous wall-clock ceiling for tests: the backoff-retry budget is
		// the real control. A tight timeout here makes the heavily-contended
		// stress tests flaky on slow/loaded CI runners.
		SelectionTimeout: 2 * time.Minute,
		Metrics:          m,
	})

	return testutils.NewEnhancedManager(t, manager, tokenDB.(dbtest.TestTokenDB)), nil
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

		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: time.Minute,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

		assert.NotNil(t, m)
		assert.Equal(t, mockLocker, m.locker)
		assert.Equal(t, 10*time.Minute, m.leaseExpiry)
		assert.Equal(t, time.Minute, m.leaseCleanupTickPeriod)
	})

	t.Run("does not start cleaner when lease expiry is zero", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            0,
			LeaseCleanupTickPeriod: time.Minute,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

		assert.NotNil(t, m)
		// Cleaner should not be started
	})

	t.Run("does not start cleaner when cleanup tick period is zero", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: 0,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

		assert.NotNil(t, m)
		// Cleaner should not be started
	})
}

func TestManager_NewSelector(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(&Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              100,
		Backoff:                time.Second,
		MaxRetriesAfterBackOff: 5,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                NewMetrics(&disabled.Provider{}),
	})

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

	m := NewManager(&Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              100,
		Backoff:                time.Second,
		MaxRetriesAfterBackOff: 5,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                NewMetrics(&disabled.Provider{}),
	})

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

	m := NewManager(&Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              100,
		Backoff:                time.Second,
		MaxRetriesAfterBackOff: 5,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                NewMetrics(&disabled.Provider{}),
	})

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
		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: 50 * time.Millisecond,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

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

		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: 50 * time.Millisecond,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

		// Wait for cleanup call (should not panic despite error)
		select {
		case <-cleanupCalled:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("cleanup not called")
		}

		// Manager should still be functional
		assert.NotNil(t, m)
	})

	t.Run("cleanup skipped when leadership not acquired", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		cleanupCalled := make(chan struct{}, 1)
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			cleanupCalled <- struct{}{}

			return nil
		}
		leadershipAttempted := make(chan struct{}, 1)
		mockLocker.acquireCleanupLeadershipFunc = func(ctx context.Context) (driver.CleanupLeadership, bool, error) {
			// Non-blocking: the ticker keeps calling this every tick, but
			// the test only reads once. A blocking send here would leave
			// the cleaner goroutine stuck inside this call on the second
			// tick, past the point where it can react to Stop().
			select {
			case leadershipAttempted <- struct{}{}:
			default:
			}

			return nil, false, nil
		}

		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: 50 * time.Millisecond,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

		select {
		case <-leadershipAttempted:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("leadership was never attempted")
		}

		select {
		case <-cleanupCalled:
			t.Fatal("Cleanup should not be called when leadership is not acquired")
		case <-time.After(150 * time.Millisecond):
			// expected: no cleanup call
		}

		require.NoError(t, m.Stop())
	})

	t.Run("leadership released after cleanup ran", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		// events records "cleanup" then "closed" in order, so the test
		// verifies Cleanup actually ran (not just that Close was called),
		// and that release happens after Cleanup completes, not before or
		// concurrently with it.
		events := make(chan string, 2)
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			events <- "cleanup"

			return nil
		}
		mockLocker.acquireCleanupLeadershipFunc = func(ctx context.Context) (driver.CleanupLeadership, bool, error) {
			return &fakeLeadership{events: events}, true, nil
		}

		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: 50 * time.Millisecond,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

		var got []string
		for range 2 {
			select {
			case e := <-events:
				got = append(got, e)
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("timed out waiting for events, got so far: %v", got)
			}
		}
		require.Equal(t, []string{"cleanup", "closed"}, got, "Cleanup must run, and leadership must release only after it completes")

		require.NoError(t, m.Stop())
	})
}

// fakeLeadership is a minimal driver.CleanupLeadership for tests, signaling
// on a channel when Close is called. If events is set, it writes "closed"
// to it (used to verify ordering relative to other recorded events).
type fakeLeadership struct {
	closed chan struct{}
	events chan string
}

func (f *fakeLeadership) Close() error {
	if f.closed != nil {
		f.closed <- struct{}{}
	}
	if f.events != nil {
		f.events <- "closed"
	}

	return nil
}

// TestManager_NewSelector_Concurrent verifies concurrent selector creation returns same instance.
func TestManager_NewSelector_Concurrent(t *testing.T) {
	mockFetcher := &mockTokenFetcher{}
	mockLocker := &mockLocker{}

	m := NewManager(&Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              100,
		Backoff:                time.Second,
		MaxRetriesAfterBackOff: 5,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                NewMetrics(&disabled.Provider{}),
	})

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

	m := NewManager(&Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              100,
		Backoff:                time.Second,
		MaxRetriesAfterBackOff: 5,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                NewMetrics(&disabled.Provider{}),
	})

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

	m := NewManager(&Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              100,
		Backoff:                time.Second,
		MaxRetriesAfterBackOff: 5,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                NewMetrics(&disabled.Provider{}),
	})

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
		m := NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            10 * time.Minute,
			LeaseCleanupTickPeriod: 10 * time.Millisecond,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

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

		NewManager(&Config{
			Fetcher:                mockFetcher,
			Locker:                 mockLocker,
			Precision:              100,
			Backoff:                time.Second,
			MaxRetriesAfterBackOff: 5,
			LeaseExpiry:            expectedExpiry,
			LeaseCleanupTickPeriod: 10 * time.Millisecond,
			MaxTokensPerSelection:  10000,
			MaxLockAttempts:        50000,
			SelectionTimeout:       30 * time.Second,
			Metrics:                NewMetrics(&disabled.Provider{}),
		})

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
			m := NewManager(&Config{
				Fetcher:                mockFetcher,
				Locker:                 mockLocker,
				Precision:              tc.precision,
				Backoff:                time.Second,
				MaxRetriesAfterBackOff: 5,
				LeaseExpiry:            0,
				LeaseCleanupTickPeriod: 0,
				MaxTokensPerSelection:  10000,
				MaxLockAttempts:        50000,
				SelectionTimeout:       30 * time.Second,
				Metrics:                NewMetrics(&disabled.Provider{}),
			})

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

func (m *mockTokenFetcher) UnspentTokensIteratorBy(ctx context.Context, walletID string, currency token2.Type, limit int) (Iterator[*token2.UnspentTokenInWallet], error) {
	if m.unspentTokensIteratorByFunc != nil {
		return m.unspentTokensIteratorByFunc(ctx, walletID, currency)
	}

	return &mockIterator{}, nil
}

type mockLocker struct {
	lockFunc                     func(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID) error
	unlockByTxIDFunc             func(ctx context.Context, consumerTxID transaction.ID) error
	unlockByTxIDCalled           bool
	cleanupFunc                  func(ctx context.Context, leaseExpiry time.Duration) error
	acquireCleanupLeadershipFunc func(ctx context.Context) (driver.CleanupLeadership, bool, error)
}

func (m *mockLocker) Lock(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID, walletID string) error {
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

// AcquireCleanupLeadership defaults to always-granted, matching the
// non-distributed backends' behavior, so existing tests that don't set
// acquireCleanupLeadershipFunc are unaffected. See #1798.
func (m *mockLocker) AcquireCleanupLeadership(ctx context.Context) (driver.CleanupLeadership, bool, error) {
	if m.acquireCleanupLeadershipFunc != nil {
		return m.acquireCleanupLeadershipFunc(ctx)
	}

	return driver.NoopCleanupLeadership{}, true, nil
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

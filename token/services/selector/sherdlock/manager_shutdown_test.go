/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManager_Stop verifies that Stop() halts the cleaner goroutine and is idempotent.
func TestManager_Stop(t *testing.T) {
	t.Run("Stop halts cleaner goroutine", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		cleanupCalls := make(chan struct{}, 10)
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error {
			cleanupCalls <- struct{}{}

			return nil
		}

		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			20*time.Millisecond,
			NewMetrics(&disabled.Provider{}),
		)

		// Wait for at least one cleanup to confirm the cleaner is running.
		select {
		case <-cleanupCalls:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("cleaner did not fire before Stop")
		}

		err := m.Stop()
		require.NoError(t, err)

		// Drain any in-flight calls that were already dispatched.
		for {
			select {
			case <-cleanupCalls:
			default:
				goto drained
			}
		}
	drained:

		// After Stop, no further cleanup calls should arrive.
		countBefore := len(cleanupCalls)
		time.Sleep(100 * time.Millisecond)
		countAfter := len(cleanupCalls)
		assert.Equal(t, countBefore, countAfter, "cleaner must not fire after Stop")
	})

	t.Run("Stop is idempotent", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}
		mockLocker.cleanupFunc = func(ctx context.Context, expiry time.Duration) error { return nil }

		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			10*time.Minute,
			20*time.Millisecond,
			NewMetrics(&disabled.Provider{}),
		)

		// Must not panic or deadlock when called multiple times.
		require.NoError(t, m.Stop())
		require.NoError(t, m.Stop())
		require.NoError(t, m.Stop())
	})

	t.Run("Stop is safe when cleaner was never started", func(t *testing.T) {
		mockFetcher := &mockTokenFetcher{}
		mockLocker := &mockLocker{}

		// Zero leaseExpiry means cleaner goroutine is never launched.
		m := NewManager(
			mockFetcher,
			mockLocker,
			100,
			time.Second,
			5,
			0,
			time.Minute,
			NewMetrics(&disabled.Provider{}),
		)

		// Must return immediately without blocking.
		done := make(chan struct{})
		go func() {
			assert.NoError(t, m.Stop())
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Stop blocked when cleaner was never started")
		}
	})
}

// TestSelectorService_Shutdown verifies that SelectorService.Shutdown stops all managers.
func TestManager_StopReturnsTimeoutError(t *testing.T) {
	m := &Manager{
		cancel:      func() {},
		cleanerDone: make(chan struct{}),
	}

	start := time.Now()
	err := m.Stop()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTimeout)
	require.GreaterOrEqual(t, time.Since(start), stopTimeout)
}

func TestSelectorService_Shutdown(t *testing.T) {
	t.Run("Shutdown stops all tracked managers", func(t *testing.T) {
		svc := &SelectorService{}
		m1 := &Manager{cancel: func() {}, cleanerDone: make(chan struct{})}
		m2 := &Manager{cancel: func() {}, cleanerDone: make(chan struct{})}
		close(m1.cleanerDone)
		close(m2.cleanerDone)

		svc.trackManager(m1)
		svc.trackManager(m2)

		// Must not panic.
		svc.Shutdown()

		assert.Equal(t, 0, svc.ManagersCount(), "managers slice cleared after Shutdown")
	})

	t.Run("Shutdown is safe with no managers", func(t *testing.T) {
		svc := &SelectorService{}
		require.NotPanics(t, svc.Shutdown)
	})
}

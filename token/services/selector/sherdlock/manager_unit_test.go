/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerUnit(t *testing.T) {
	mockFetcher := &mocks.FakeTokenFetcher{}
	mockLocker := &mocks.FakeLocker{}
	_, metrics := setupMetricsMocks()

	mgr := sherdlock.NewManager(&sherdlock.Config{
		Fetcher:                mockFetcher,
		Locker:                 mockLocker,
		Precision:              64,
		Backoff:                0,
		MaxRetriesAfterBackOff: 0,
		LeaseExpiry:            0,
		LeaseCleanupTickPeriod: 0,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		SelectionTimeout:       30 * time.Second,
		Metrics:                metrics,
	})
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

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"testing"

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

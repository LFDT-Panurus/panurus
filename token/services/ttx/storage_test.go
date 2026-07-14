/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests storage.go which provides storage interfaces and utilities for token transactions.
// Tests verify proper storage provider retrieval, transaction record storage, and error handling.
package ttx_test

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetStorageProvider_Success verifies successful storage provider retrieval.
func TestGetStorageProvider_Success(t *testing.T) {
	mockCtx := &mock.Context{}
	mockStorageProvider := &mock.StorageProvider{}

	mockCtx.GetServiceReturns(mockStorageProvider, nil)

	result, err := ttx.GetStorageProvider(mockCtx)

	require.NoError(t, err)
	assert.Equal(t, mockStorageProvider, result)
	assert.Equal(t, 1, mockCtx.GetServiceCallCount())
}

// TestGetStorageProvider_Error verifies error handling when service retrieval fails.
func TestGetStorageProvider_Error(t *testing.T) {
	mockCtx := &mock.Context{}
	expectedErr := errors.New("service not found")

	mockCtx.GetServiceReturns(nil, expectedErr)

	result, err := ttx.GetStorageProvider(mockCtx)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, expectedErr, err)
}

// TestStoreTransactionRecords_Success verifies successful transaction record storage.
func TestStoreTransactionRecords_Success(t *testing.T) {
	mockCtx := &mock.Context{}
	mockStorageProvider := &mock.StorageProvider{}
	mockStorage := &mock.Storage{}
	mockTMS := &mock.TokenManagementServiceWithExtensions{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	tx := &ttx.Transaction{
		TMS: mockTMS,
	}

	// Setup mocks
	mockCtx.GetServiceReturns(mockStorageProvider, nil)
	mockTMS.IDReturns(tmsID)
	mockStorageProvider.GetStorageReturns(mockStorage, nil)
	mockStorage.AppendReturns(nil)
	mockCtx.ContextReturns(t.Context())

	err := ttx.StoreTransactionRecords(mockCtx, tx)

	require.NoError(t, err)
	assert.Equal(t, 1, mockCtx.GetServiceCallCount())
	assert.Equal(t, 1, mockTMS.IDCallCount())
	assert.Equal(t, 1, mockStorageProvider.GetStorageCallCount())
	assert.Equal(t, 1, mockStorage.AppendCallCount())

	// Verify correct TMS ID was passed
	passedTMSID := mockStorageProvider.GetStorageArgsForCall(0)
	assert.Equal(t, tmsID, passedTMSID)

	// Verify correct transaction was passed
	passedCtx, passedTx := mockStorage.AppendArgsForCall(0)
	assert.Equal(t, t.Context(), passedCtx)
	assert.Equal(t, tx, passedTx)
}

// TestStoreTransactionRecords_GetStorageProviderError verifies error when storage provider retrieval fails.
func TestStoreTransactionRecords_GetStorageProviderError(t *testing.T) {
	mockCtx := &mock.Context{}
	mockTMS := &mock.TokenManagementServiceWithExtensions{}
	expectedErr := errors.New("storage provider not found")

	tx := &ttx.Transaction{
		TMS: mockTMS,
	}

	mockCtx.GetServiceReturns(nil, expectedErr)

	err := ttx.StoreTransactionRecords(mockCtx, tx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage")
	assert.Equal(t, 1, mockCtx.GetServiceCallCount())
	assert.Equal(t, 0, mockTMS.IDCallCount()) // Should not reach this point
}

// TestStoreTransactionRecords_GetStorageError verifies error when storage retrieval fails.
func TestStoreTransactionRecords_GetStorageError(t *testing.T) {
	mockCtx := &mock.Context{}
	mockStorageProvider := &mock.StorageProvider{}
	mockTMS := &mock.TokenManagementServiceWithExtensions{}
	expectedErr := errors.New("storage not found")

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	tx := &ttx.Transaction{
		TMS: mockTMS,
	}

	mockCtx.GetServiceReturns(mockStorageProvider, nil)
	mockTMS.IDReturns(tmsID)
	mockStorageProvider.GetStorageReturns(nil, expectedErr)

	err := ttx.StoreTransactionRecords(mockCtx, tx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage")
	assert.Equal(t, 1, mockStorageProvider.GetStorageCallCount())
}

// TestStoreTransactionRecords_AppendError verifies error when append operation fails.
func TestStoreTransactionRecords_AppendError(t *testing.T) {
	mockCtx := &mock.Context{}
	mockStorageProvider := &mock.StorageProvider{}
	mockStorage := &mock.Storage{}
	mockTMS := &mock.TokenManagementServiceWithExtensions{}
	expectedErr := errors.New("append failed")

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	tx := &ttx.Transaction{
		TMS: mockTMS,
	}

	mockCtx.GetServiceReturns(mockStorageProvider, nil)
	mockTMS.IDReturns(tmsID)
	mockStorageProvider.GetStorageReturns(mockStorage, nil)
	mockStorage.AppendReturns(expectedErr)
	mockCtx.ContextReturns(t.Context())

	err := ttx.StoreTransactionRecords(mockCtx, tx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage")
	assert.Equal(t, 1, mockStorage.AppendCallCount())
}

// TestStoreTransactionRecords_WithDifferentTMSIDs verifies handling of different TMS IDs.
func TestStoreTransactionRecords_WithDifferentTMSIDs(t *testing.T) {
	testCases := []struct {
		name  string
		tmsID token.TMSID
	}{
		{
			name: "full TMS ID",
			tmsID: token.TMSID{
				Network:   "network1",
				Channel:   "channel1",
				Namespace: "namespace1",
			},
		},
		{
			name: "empty namespace",
			tmsID: token.TMSID{
				Network: "network2",
				Channel: "channel2",
			},
		},
		{
			name: "empty channel",
			tmsID: token.TMSID{
				Network:   "network3",
				Namespace: "namespace3",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtx := &mock.Context{}
			mockStorageProvider := &mock.StorageProvider{}
			mockStorage := &mock.Storage{}
			mockTMS := &mock.TokenManagementServiceWithExtensions{}

			tx := &ttx.Transaction{
				TMS: mockTMS,
			}

			mockCtx.GetServiceReturns(mockStorageProvider, nil)
			mockTMS.IDReturns(tc.tmsID)
			mockStorageProvider.GetStorageReturns(mockStorage, nil)
			mockStorage.AppendReturns(nil)
			mockCtx.ContextReturns(t.Context())

			err := ttx.StoreTransactionRecords(mockCtx, tx)

			require.NoError(t, err)
			passedTMSID := mockStorageProvider.GetStorageArgsForCall(0)
			assert.Equal(t, tc.tmsID, passedTMSID)
		})
	}
}

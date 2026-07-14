/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	drivermock "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/ttx/finality/mock"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestTTXRecoveryHandler_Recover_ValidTransaction_CachedRequest(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx123"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	mockTx := &drivermock.TransactionStoreTransaction{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil, // metrics provider is nil-safe
	)

	// Setup test data
	msgToSign := []byte("message")
	// Compute the expected hash and decode it to get the raw bytes
	expectedHashString := utils.Hashable(msgToSign).String()
	tokenRequestHash, err := base64.StdEncoding.DecodeString(expectedHashString)
	require.NoError(t, err)
	mockRequest := &token.Request{}

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Valid, tokenRequestHash, "", nil)
	mockTokens.GetCachedTokenRequestReturns(mockRequest, msgToSign)
	mockTTXDB.NewTransactionReturns(mockTx, nil)
	mockTokens.AppendValidReturns(nil)
	mockTx.SetStatusReturns(nil)
	mockTx.CommitReturns(nil)
	mockTTXDB.SetStatusReturns(nil)

	// Execute
	recoverErr := handler.Recover(ctx, txID)

	// Verify
	require.NoError(t, recoverErr)
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 1, mockTokens.GetCachedTokenRequestCallCount())
	require.Equal(t, 1, mockTTXDB.NewTransactionCallCount())
	require.Equal(t, 1, mockTokens.AppendValidCallCount())
	require.Equal(t, 1, mockTx.SetStatusCallCount())
	require.Equal(t, 1, mockTx.CommitCallCount())
	require.Equal(t, 0, mockTTXDB.SetStatusCallCount())
}

func TestTTXRecoveryHandler_Recover_ValidTransaction_LoadFromDB(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx456"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	mockTx := &drivermock.TransactionStoreTransaction{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Setup test data
	tokenRequestRaw := []byte("raw_request")
	msgToSign := []byte("message")
	expectedHashString := utils.Hashable(msgToSign).String()
	tokenRequestHash, err := base64.StdEncoding.DecodeString(expectedHashString)
	require.NoError(t, err)
	mockRequest := &token.Request{}

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Valid, tokenRequestHash, "", nil)
	mockTokens.GetCachedTokenRequestReturns(nil, nil) // Not cached
	mockTTXDB.GetTokenRequestReturns(tokenRequestRaw, nil)
	mockHasher.ProcessTokenRequestReturns(mockRequest, msgToSign, nil)
	mockTTXDB.NewTransactionReturns(mockTx, nil)
	mockTokens.AppendValidReturns(nil)
	mockTx.SetStatusReturns(nil)
	mockTx.CommitReturns(nil)
	mockTTXDB.SetStatusReturns(nil)

	// Execute
	err = handler.Recover(ctx, txID)

	// Verify
	require.NoError(t, err)
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 1, mockTokens.GetCachedTokenRequestCallCount())
	require.Equal(t, 1, mockTTXDB.GetTokenRequestCallCount())
	require.Equal(t, 1, mockHasher.ProcessTokenRequestCallCount())
	require.Equal(t, 1, mockTTXDB.NewTransactionCallCount())
	require.Equal(t, 1, mockTokens.AppendValidCallCount())
	require.Equal(t, 1, mockTx.SetStatusCallCount())
	require.Equal(t, 1, mockTx.CommitCallCount())
	require.Equal(t, 0, mockTTXDB.SetStatusCallCount())
}

func TestTTXRecoveryHandler_Recover_InvalidTransaction(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx789"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Invalid, nil, "invalid tx", nil)
	mockTTXDB.SetStatusReturns(nil)

	// Execute
	err := handler.Recover(ctx, txID)

	// Verify
	require.NoError(t, err)
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 0, mockTokens.GetCachedTokenRequestCallCount()) // Should not check cache for invalid tx
	require.Equal(t, 1, mockTTXDB.SetStatusCallCount())

	// Verify SetStatus was called with Deleted
	_, actualTxID, actualStatus, _ := mockTTXDB.SetStatusArgsForCall(0)
	require.Equal(t, txID, actualTxID)
	require.Equal(t, storage.Deleted, actualStatus)
}

func TestTTXRecoveryHandler_Recover_BusyTransaction(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx_busy"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Mock expectations - Busy status
	mockNetwork.GetTransactionStatusReturns(network.Busy, nil, "", nil)

	// Execute
	err := handler.Recover(ctx, txID)

	// Verify - should return nil (graceful handling) for non-finalized transaction
	// This allows the transaction to be retried on the next scan without error churn
	require.NoError(t, err)
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 0, mockTTXDB.SetStatusCallCount()) // Should not update status for Busy
}

func TestTTXRecoveryHandler_Recover_NetworkError(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx_error"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Mock expectations - network error
	expectedErr := errors.New("network connection failed")
	mockNetwork.GetTransactionStatusReturns(0, nil, "", expectedErr)

	// Execute
	err := handler.Recover(ctx, txID)

	// Verify
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get transaction status")
	require.Contains(t, err.Error(), "network connection failed")
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
}

func TestTTXRecoveryHandler_Recover_HashMismatch(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx_hash_mismatch"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Setup test data with mismatched hashes
	tokenRequestHash := []byte("wrong_hash")
	msgToSign := []byte("message")
	mockRequest := &token.Request{}

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Valid, tokenRequestHash, "", nil)
	mockTokens.GetCachedTokenRequestReturns(mockRequest, msgToSign)
	mockTTXDB.SetStatusReturns(nil)

	// Execute
	err := handler.Recover(ctx, txID)

	// Verify
	require.NoError(t, err) // Should not error, but mark as Deleted
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 1, mockTokens.GetCachedTokenRequestCallCount())
	require.Equal(t, 0, mockTokens.AppendValidCallCount()) // Should not append due to hash mismatch
	require.Equal(t, 1, mockTTXDB.SetStatusCallCount())

	// Verify SetStatus was called with Deleted due to hash mismatch
	_, actualTxID, actualStatus, actualMessage := mockTTXDB.SetStatusArgsForCall(0)
	require.Equal(t, txID, actualTxID)
	require.Equal(t, storage.Deleted, actualStatus)
	require.Contains(t, actualMessage, "token requests do not match")
}

func TestTTXRecoveryHandler_Recover_GetTokenRequestError(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx_db_error"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Valid, []byte("hash"), "", nil)
	mockTokens.GetCachedTokenRequestReturns(nil, nil) // Not cached
	expectedErr := errors.New("database error")
	mockTTXDB.GetTokenRequestReturns(nil, expectedErr)

	// Execute
	err := handler.Recover(ctx, txID)

	// Verify
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed retrieving token request")
	require.Contains(t, err.Error(), "database error")
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 1, mockTTXDB.GetTokenRequestCallCount())
}

func TestTTXRecoveryHandler_Recover_AppendError(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx_append_error"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	mockTx := &drivermock.TransactionStoreTransaction{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Setup test data
	msgToSign := []byte("message")
	// Compute the expected hash and decode it to get the raw bytes
	expectedHashString := utils.Hashable(msgToSign).String()
	tokenRequestHash, err := base64.StdEncoding.DecodeString(expectedHashString)
	require.NoError(t, err)
	mockRequest := &token.Request{}

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Valid, tokenRequestHash, "", nil)
	mockTokens.GetCachedTokenRequestReturns(mockRequest, msgToSign)
	mockTTXDB.NewTransactionReturns(mockTx, nil)
	expectedErr := errors.New("append failed")
	mockTokens.AppendValidReturns(expectedErr)

	// Execute
	recoverErr := handler.Recover(ctx, txID)

	// Verify
	require.Error(t, recoverErr)
	require.Contains(t, recoverErr.Error(), "failed to append valid token request to token db")
	require.Contains(t, recoverErr.Error(), "append failed")
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 1, mockTTXDB.NewTransactionCallCount())
	require.Equal(t, 1, mockTokens.AppendValidCallCount())
	require.Equal(t, 0, mockTTXDB.SetStatusCallCount()) // Should not update status due to append error
}

func TestTTXRecoveryHandler_Recover_SetStatusError(t *testing.T) {
	// Setup
	ctx := context.Background()
	txID := "tx_setstatus_error"
	namespace := "testns"
	tmsID := token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

	// Create mocks
	mockNetwork := &mock2.Network{}
	mockHasher := &mock2.TokenRequestHasher{}
	mockTTXDB := &mock2.TransactionDB{}
	mockTokens := &mock2.TokensService{}
	logger := logging.MustGetLogger()
	tracer := noop.NewTracerProvider().Tracer("test")

	// Create handler
	handler := finality.NewTTXRecoveryHandler(
		logger,
		mockNetwork,
		namespace,
		mockHasher,
		tmsID,
		mockTTXDB,
		mockTokens,
		tracer,
		nil,
	)

	// Mock expectations
	mockNetwork.GetTransactionStatusReturns(network.Invalid, nil, "invalid", nil)
	expectedErr := errors.New("set status failed")
	mockTTXDB.SetStatusReturns(expectedErr)

	// Execute
	err := handler.Recover(ctx, txID)

	// Verify
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to set status")
	require.Contains(t, err.Error(), "set status failed")
	require.Equal(t, 1, mockNetwork.GetTransactionStatusCallCount())
	require.Equal(t, 1, mockTTXDB.SetStatusCallCount())
}

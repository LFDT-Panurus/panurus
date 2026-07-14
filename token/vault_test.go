/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token tests vault.go which provides token vault query and storage functionality.
package token

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
)

// TestQueryEngine_IsMine verifies that IsMine correctly identifies owned tokens
func TestQueryEngine_IsMine(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedID := &token.ID{TxId: "a_transaction", Index: 0}
	mockQE.IsMineReturns(true, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	isMine, err := queryEngine.IsMine(t.Context(), expectedID)

	require.NoError(t, err)
	assert.True(t, isMine)
}

// TestQueryEngine_IsMine_Error verifies error handling when IsMine fails
func TestQueryEngine_IsMine_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.IsMineReturns(false, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	isMine, err := queryEngine.IsMine(t.Context(), nil)

	require.Error(t, err)
	assert.False(t, isMine)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_ListAuditTokens verifies listing audit tokens with default parameters
func TestQueryEngine_ListAuditTokens(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedIDs := []*token.ID{{TxId: "a_transaction", Index: 0}}
	expectedTokens := []*token.Token{{
		Owner:    []byte("some_owner"),
		Type:     "some_type",
		Quantity: "some_quantity",
	}}
	mockQE.ListAuditTokensReturns(expectedTokens, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	tokens, err := queryEngine.ListAuditTokens(t.Context(), expectedIDs...)
	require.NoError(t, err)
	assert.Equal(t, expectedTokens, tokens)
	mockQE.ListAuditTokensReturnsOnCall(0, nil, errors.New("pending transactions"))
	mockQE.ListAuditTokensReturnsOnCall(1, expectedTokens, nil)

	tokens, err = queryEngine.ListAuditTokens(t.Context(), expectedIDs...)
	require.NoError(t, err)
	assert.Equal(t, expectedTokens, tokens)
	mockQE.ListAuditTokensReturns(nil, errors.New("pending transactions"))

	tokens, err = queryEngine.ListAuditTokens(t.Context(), expectedIDs...)
	require.Error(t, err)
	assert.Nil(t, tokens)
	require.EqualError(t, err, "failed to get audit tokens: pending transactions")
}

// TestQueryEngine_ListAuditTokens_IsPendingTrue verifies listing pending audit tokens
func TestQueryEngine_ListAuditTokens_IsPendingTrue(t *testing.T) {
	ctx := t.Context()
	mockQE := &mock.QueryEngine{}
	expectedIDs := []*token.ID{{TxId: "a_transaction", Index: 0}}
	expectedTokens := []*token.Token{{
		Owner:    []byte("some_owner"),
		Type:     "some_type",
		Quantity: "some_quantity",
	}}
	mockQE.ListAuditTokensReturnsOnCall(0, nil, errors.New("not found"))
	mockQE.ListAuditTokensReturnsOnCall(1, expectedTokens, nil)
	mockQE.IsPendingReturnsOnCall(0, true, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	tokens, err := queryEngine.ListAuditTokens(ctx, expectedIDs...)

	require.NoError(t, err)
	assert.Equal(t, expectedTokens, tokens)
	assert.Equal(t, 1, mockQE.IsPendingCallCount())
	_, id := mockQE.IsPendingArgsForCall(0)
	assert.Equal(t, expectedIDs[0], id)
	assert.Equal(t, 2, mockQE.ListAuditTokensCallCount())
}

// TestQueryEngine_ListAuditTokens_IsPendingTrueNumRetries verifies retry logic for pending audit tokens
func TestQueryEngine_ListAuditTokens_IsPendingTrueNumRetries(t *testing.T) {
	ctx := t.Context()
	mockQE := &mock.QueryEngine{}
	expectedIDs := []*token.ID{{TxId: "a_transaction", Index: 0}}
	mockQE.ListAuditTokensReturnsOnCall(0, nil, errors.New("not found"))
	mockQE.ListAuditTokensReturnsOnCall(1, nil, errors.New("not found"))
	mockQE.ListAuditTokensReturnsOnCall(2, nil, errors.New("not found"))
	mockQE.ListAuditTokensReturnsOnCall(3, nil, errors.New("not found"))
	mockQE.IsPendingReturnsOnCall(0, true, nil)
	mockQE.IsPendingReturnsOnCall(1, true, nil)
	mockQE.IsPendingReturnsOnCall(2, true, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	tokens, err := queryEngine.ListAuditTokens(ctx, expectedIDs...)

	require.Error(t, err)
	assert.Empty(t, tokens)
	assert.Equal(t, 3, mockQE.IsPendingCallCount())
	_, id := mockQE.IsPendingArgsForCall(0)
	assert.Equal(t, expectedIDs[0], id)
	_, id = mockQE.IsPendingArgsForCall(1)
	assert.Equal(t, expectedIDs[0], id)
	_, id = mockQE.IsPendingArgsForCall(2)
	assert.Equal(t, expectedIDs[0], id)
	assert.Equal(t, 3, mockQE.ListAuditTokensCallCount())
}

// TestQueryEngine_UnspentTokensIterator_Error verifies error handling when creating unspent tokens iterator
func TestQueryEngine_UnspentTokensIterator_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.UnspentTokensIteratorReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	iterator, err := queryEngine.UnspentTokensIterator(t.Context())
	require.Error(t, err)
	assert.Nil(t, iterator)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_ListUnspentTokens verifies listing unspent tokens
func TestQueryEngine_ListUnspentTokens(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedUnspentTokens := &token.UnspentTokens{}
	mockQE.ListUnspentTokensReturns(expectedUnspentTokens, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	unspentTokens, err := queryEngine.ListUnspentTokens(t.Context())
	require.NoError(t, err)
	assert.Equal(t, expectedUnspentTokens, unspentTokens)
}

// TestQueryEngine_ListUnspentTokens_Error verifies error handling when listing unspent tokens fails
func TestQueryEngine_ListUnspentTokens_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.ListUnspentTokensReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	unspentTokens, err := queryEngine.ListUnspentTokens(t.Context())
	require.Error(t, err)
	assert.Nil(t, unspentTokens)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_ListHistoryIssuedTokens verifies listing historical issued tokens
func TestQueryEngine_ListHistoryIssuedTokens(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedIssuedTokens := &token.IssuedTokens{}
	mockQE.ListHistoryIssuedTokensReturns(expectedIssuedTokens, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	issuedTokens, err := queryEngine.ListHistoryIssuedTokens(t.Context())

	require.NoError(t, err)
	assert.Equal(t, expectedIssuedTokens, issuedTokens)
}

// TestQueryEngine_ListHistoryIssuedTokens_Error verifies error handling when listing issued tokens fails
func TestQueryEngine_ListHistoryIssuedTokens_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.ListHistoryIssuedTokensReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	issuedTokens, err := queryEngine.ListHistoryIssuedTokens(t.Context())

	require.Error(t, err)
	assert.Nil(t, issuedTokens)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_PublicParams verifies retrieval of public parameters
func TestQueryEngine_PublicParams(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedParams := []byte("public parameters")
	mockQE.PublicParamsReturns(expectedParams, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	params, err := queryEngine.PublicParams(t.Context())

	require.NoError(t, err)
	assert.Equal(t, expectedParams, params)
}

// TestQueryEngine_PublicParams_Error verifies error handling when retrieving public parameters fails
func TestQueryEngine_PublicParams_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.PublicParamsReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	params, err := queryEngine.PublicParams(t.Context())

	require.Error(t, err)
	assert.Nil(t, params)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_GetTokens verifies retrieval of specific tokens by IDs
func TestQueryEngine_GetTokens(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedTokens := []*token.Token{{
		Owner:    []byte("some_owner"),
		Type:     "some_type",
		Quantity: "some_quantity",
	}}
	mockQE.GetTokensReturns(expectedTokens, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	tokens, err := queryEngine.GetTokens(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, expectedTokens, tokens)
}

// TestQueryEngine_GetTokens_Error verifies error handling when getting tokens fails
func TestQueryEngine_GetTokens_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}

	expectedErr := errors.New("mock error")
	mockQE.GetTokensReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	tokens, err := queryEngine.GetTokens(t.Context(), nil)
	require.Error(t, err)
	assert.Nil(t, tokens)
	assert.Equal(t, expectedErr, err)
}

// TestCertificationStorage_Exists verifies checking if a certification exists
func TestCertificationStorage_Exists(t *testing.T) {
	mockStorage := &mock.CertificationStorage{}
	id := &token.ID{}
	mockStorage.ExistsReturns(true)

	certStorage := &CertificationStorage{c: mockStorage}
	exists := certStorage.Exists(t.Context(), id)
	assert.True(t, exists, "Expected certification to exist")
	assert.Equal(t, 1, mockStorage.ExistsCallCount(), "Exists method should be called once")
	_, id2 := mockStorage.ExistsArgsForCall(0)
	assert.Equal(t, id, id2, "Exists method should be called with the correct argument")
}

// TestCertificationStorage_Exists_NotExist verifies behavior when certification does not exist
func TestCertificationStorage_Exists_NotExist(t *testing.T) {
	mockStorage := &mock.CertificationStorage{}
	expectedID := &token.ID{TxId: "a_transaction", Index: 0}
	mockStorage.ExistsReturns(false)

	certStorage := &CertificationStorage{c: mockStorage}
	exists := certStorage.Exists(t.Context(), expectedID)
	assert.False(t, exists, "Expected certification not to exist")
	assert.Equal(t, 1, mockStorage.ExistsCallCount(), "Exists method should be called once")
	_, id := mockStorage.ExistsArgsForCall(0)
	assert.Equal(t, expectedID, id, "Exists method should be called with the correct argument")
}

// TestCertificationStorage_Store verifies storing a certification
func TestCertificationStorage_Store(t *testing.T) {
	mockStorage := &mock.CertificationStorage{}
	certifications := map[*token.ID][]byte{
		{TxId: "a_transaction", Index: 0}:       []byte("cert1"),
		{TxId: "another_transaction", Index: 0}: []byte("cert2"),
	}
	mockStorage.StoreReturns(nil)

	certStorage := &CertificationStorage{c: mockStorage}
	err := certStorage.Store(t.Context(), certifications)
	require.NoError(t, err, "Expected no error while storing certifications")
	assert.Equal(t, 1, mockStorage.StoreCallCount(), "Store method should be called once")
	_, id := mockStorage.StoreArgsForCall(0)
	assert.Equal(t, certifications, id, "Store method should be called with the correct argument")
}

// TestCertificationStorage_Store_Error verifies error handling when storing certification fails
func TestCertificationStorage_Store_Error(t *testing.T) {
	mockStorage := &mock.CertificationStorage{}
	certifications := map[*token.ID][]byte{
		{TxId: "a_transaction", Index: 0}:       []byte("cert1"),
		{TxId: "another_transaction", Index: 0}: []byte("cert2"),
	}
	mockErr := errors.New("storage error")
	mockStorage.StoreReturns(mockErr)

	certStorage := &CertificationStorage{c: mockStorage}
	err := certStorage.Store(t.Context(), certifications)
	require.Error(t, err, "Expected an error while storing certifications")
	require.EqualError(t, err, mockErr.Error(), "Expected the same error returned by the storage")
	assert.Equal(t, 1, mockStorage.StoreCallCount(), "Store method should be called once")
	_, id := mockStorage.StoreArgsForCall(0)
	assert.Equal(t, certifications, id, "Store method should be called with the correct argument")
}

// TestUnspentTokensIterator_Sum verifies calculating sum of unspent tokens
func TestUnspentTokensIterator_Sum(t *testing.T) {
	mockIterator := &mock.UnspentTokensIterator{}
	staticToken := &token.UnspentToken{Quantity: "10"}
	staticToken2 := &token.UnspentToken{Quantity: "20"}
	mockIterator.NextReturnsOnCall(0, staticToken, nil)
	mockIterator.NextReturnsOnCall(1, staticToken2, nil)
	mockIterator.NextReturnsOnCall(2, nil, nil)

	sum, err := iterators.Reduce[token.UnspentToken](mockIterator, token.ToQuantitySum(64))
	require.NoError(t, err, "Expected no error while summing tokens")
	assert.NotNil(t, sum, "Expected a non-nil sum")
	expectedSum := token.NewQuantityFromUInt64(30)
	assert.Equal(t, 0, expectedSum.Cmp(sum), "Expected sum to be equal to 30")
	assert.Equal(t, 3, mockIterator.NextCallCount(), "Next method should be called three times")
	assert.Equal(t, 1, mockIterator.CloseCallCount(), "Close method should be called once")
}

// TestUnspentTokensIterator_Sum_ErrorInNext verifies error handling when iterator Next fails
func TestUnspentTokensIterator_Sum_ErrorInNext(t *testing.T) {
	mockIterator := &mock.UnspentTokensIterator{}
	mockErr := errors.New("iterator error")
	mockIterator.NextReturns(nil, mockErr)
	mockIterator.CloseCalls(func() {})

	sum, err := iterators.Reduce[token.UnspentToken](mockIterator, token.ToQuantitySum(64))
	assert.Nil(t, sum, "Expected a nil sum when Next returns an error")
	require.Error(t, err, "Expected an error when Next returns an error")
	require.EqualError(t, err, mockErr.Error(), "Expected the same error returned by Next")
	assert.Equal(t, 1, mockIterator.NextCallCount(), "Next method should be called once")
	assert.Equal(t, 1, mockIterator.CloseCallCount(), "Close method should be called")
}

// TestUnspentTokensIterator_Sum_ErrorInToQuantity verifies error handling when ToQuantity conversion fails
func TestUnspentTokensIterator_Sum_ErrorInToQuantity(t *testing.T) {
	mockIterator := &mock.UnspentTokensIterator{}
	mockIterator.NextReturns(&token.UnspentToken{Quantity: "invalid"}, nil)
	mockIterator.CloseCalls(func() {})

	sum, err := iterators.Reduce[token.UnspentToken](mockIterator, token.ToQuantitySum(64))
	assert.Nil(t, sum, "Expected a nil sum when ToQuantity fails")
	require.Error(t, err, "Expected an error when ToQuantity fails")
	assert.Equal(t, 1, mockIterator.NextCallCount(), "Next method should be called once")
	assert.Equal(t, 1, mockIterator.CloseCallCount(), "Close method should be called")
}

// TestQueryEngine_UnspentTokensIteratorBy verifies iterator creation for unspent tokens
func TestQueryEngine_UnspentTokensIteratorBy(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	mockIterator := &mock.UnspentTokensIterator{}
	mockQE.UnspentTokensIteratorByReturns(mockIterator, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	iterator, err := queryEngine.UnspentTokensIteratorBy(t.Context(), "wallet1", "USD")

	require.NoError(t, err)
	assert.NotNil(t, iterator)
	assert.Equal(t, 1, mockQE.UnspentTokensIteratorByCallCount())
}

// TestQueryEngine_UnspentTokensIteratorBy_Error verifies error handling in iterator creation
func TestQueryEngine_UnspentTokensIteratorBy_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.UnspentTokensIteratorByReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	iterator, err := queryEngine.UnspentTokensIteratorBy(t.Context(), "wallet1", "USD")

	require.Error(t, err)
	assert.Nil(t, iterator)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_GetStatus verifies transaction status retrieval
func TestQueryEngine_GetStatus(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedStatus := Confirmed
	expectedMessage := "transaction confirmed"
	mockQE.GetStatusReturns(expectedStatus, expectedMessage, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	status, message, err := queryEngine.GetStatus(t.Context(), "tx123")

	require.NoError(t, err)
	assert.Equal(t, expectedStatus, status)
	assert.Equal(t, expectedMessage, message)
}

// TestQueryEngine_GetStatus_Error verifies error handling in status retrieval
func TestQueryEngine_GetStatus_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.GetStatusReturns(Unknown, "", expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	status, message, err := queryEngine.GetStatus(t.Context(), "tx123")

	require.Error(t, err)
	assert.Equal(t, Unknown, status)
	assert.Empty(t, message)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_GetTokenOutputs verifies token output retrieval
func TestQueryEngine_GetTokenOutputs(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	mockQE.GetTokenOutputsReturns(nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	err := queryEngine.GetTokenOutputs(t.Context(), ids, func(id *token.ID, tokenRaw []byte) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, mockQE.GetTokenOutputsCallCount())
}

// TestQueryEngine_GetTokenOutputs_Error verifies error handling in token output retrieval
func TestQueryEngine_GetTokenOutputs_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	mockQE.GetTokenOutputsReturns(expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	err := queryEngine.GetTokenOutputs(t.Context(), ids, func(id *token.ID, tokenRaw []byte) error {
		return nil
	})

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_WhoDeletedTokens verifies retrieval of token deletion info
func TestQueryEngine_WhoDeletedTokens(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	expectedWho := []string{"user1"}
	expectedDeleted := []bool{true}
	mockQE.WhoDeletedTokensReturns(expectedWho, expectedDeleted, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	who, deleted, err := queryEngine.WhoDeletedTokens(t.Context(), ids...)

	require.NoError(t, err)
	assert.Equal(t, expectedWho, who)
	assert.Equal(t, expectedDeleted, deleted)
}

// TestQueryEngine_WhoDeletedTokens_Error verifies error handling in deletion info retrieval
func TestQueryEngine_WhoDeletedTokens_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	mockQE.WhoDeletedTokensReturns(nil, nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	who, deleted, err := queryEngine.WhoDeletedTokens(t.Context(), ids...)

	require.Error(t, err)
	assert.Nil(t, who)
	assert.Nil(t, deleted)
	assert.Equal(t, expectedErr, err)
}

// TestQueryEngine_UnspentLedgerTokensIteratorBy verifies ledger token iterator creation
func TestQueryEngine_UnspentLedgerTokensIteratorBy(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	mockQE.UnspentLedgerTokensIteratorByReturns(nil, nil)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	_, err := queryEngine.UnspentLedgerTokensIteratorBy(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 1, mockQE.UnspentLedgerTokensIteratorByCallCount())
}

// TestQueryEngine_UnspentLedgerTokensIteratorBy_Error verifies error handling in ledger iterator creation
func TestQueryEngine_UnspentLedgerTokensIteratorBy_Error(t *testing.T) {
	mockQE := &mock.QueryEngine{}
	expectedErr := errors.New("mock error")
	mockQE.UnspentLedgerTokensIteratorByReturns(nil, expectedErr)

	queryEngine := NewQueryEngine(logging.MustGetLogger(), mockQE, 3, time.Second)
	iterator, err := queryEngine.UnspentLedgerTokensIteratorBy(t.Context())

	require.Error(t, err)
	assert.Nil(t, iterator)
	assert.Equal(t, expectedErr, err)
}

// TestCertificationStorage_Exists_NilStorage verifies behavior when storage is nil
func TestCertificationStorage_Exists_NilStorage(t *testing.T) {
	certStorage := &CertificationStorage{c: nil}
	exists := certStorage.Exists(t.Context(), &token.ID{})
	assert.False(t, exists)
}

// TestCertificationStorage_Store_NilStorage verifies error when storing to nil storage
func TestCertificationStorage_Store_NilStorage(t *testing.T) {
	certStorage := &CertificationStorage{c: nil}
	err := certStorage.Store(t.Context(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certification storage is not supported")
}

// TestVault_NewQueryEngine verifies query engine creation from vault
func TestVault_NewQueryEngine(t *testing.T) {
	mockVault := &mock.Vault{}
	mockQE := &mock.QueryEngine{}
	mockVault.QueryEngineReturns(mockQE)

	vault := &Vault{
		v:      mockVault,
		logger: logging.MustGetLogger(),
	}

	qe := vault.NewQueryEngine()
	assert.NotNil(t, qe)
	assert.Equal(t, mockQE, qe.qe)
	assert.Equal(t, 3, qe.NumRetries)
	assert.Equal(t, 3*time.Second, qe.RetryDelay)
}

// TestVault_CertificationStorage verifies certification storage accessor
func TestVault_CertificationStorage(t *testing.T) {
	mockCertStorage := &mock.CertificationStorage{}
	vault := &Vault{
		certificationStorage: &CertificationStorage{c: mockCertStorage},
	}

	cs := vault.CertificationStorage()
	assert.NotNil(t, cs)
	assert.Equal(t, mockCertStorage, cs.c)
}

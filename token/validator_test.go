/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidator_UnmarshalActions verifies unmarshaling token actions from raw bytes
func TestValidator_UnmarshalActions(t *testing.T) {
	validator := &Validator{
		backend: &mock.Validator{},
	}

	raw := []byte("some_raw_data")

	expectedActions := []any{"action1", "action2"}
	mockValidator := validator.backend.(*mock.Validator)
	mockValidator.UnmarshalActionsReturns(expectedActions, nil)

	actions, err := validator.UnmarshalActions(raw)

	require.NoError(t, err)
	assert.Equal(t, expectedActions, actions)
}

// TestValidator_UnmarshallAndVerify verifies unmarshaling and verifying token request
func TestValidator_UnmarshallAndVerify(t *testing.T) {
	validator := &Validator{
		backend: &mock.Validator{},
	}
	mockLedger := &mock.ValidatorLedger{}
	raw := []byte("some_raw_data")
	anchor := "some_anchor"

	expectedActions := []any{"action1", "action2"}
	mockValidator := validator.backend.(*mock.Validator)
	mockValidator.VerifyTokenRequestFromRawReturns(expectedActions, nil, nil)

	actions, err := validator.UnmarshallAndVerify(t.Context(), mockLedger, RequestAnchor(anchor), raw)

	require.NoError(t, err)
	assert.Equal(t, expectedActions, actions)
}

// TestValidator_UnmarshallAndVerifyWithMetadata verifies unmarshaling and verifying token request with metadata
func TestValidator_UnmarshallAndVerifyWithMetadata(t *testing.T) {
	validator := &Validator{
		backend: &mock.Validator{},
	}
	mockLedger := &mock.ValidatorLedger{}
	raw := []byte("some_raw_data")
	anchor := "some_anchor"

	expectedActions := []any{"action1", "action2"}
	expectedMetadata := map[string][]byte{"key1": []byte("value1"), "key2": []byte("value2")}
	mockValidator := validator.backend.(*mock.Validator)
	mockValidator.VerifyTokenRequestFromRawReturns(expectedActions, expectedMetadata, nil)
	actions, metadata, err := validator.UnmarshallAndVerifyWithMetadata(t.Context(), mockLedger, RequestAnchor(anchor), raw)

	require.NoError(t, err)
	assert.Equal(t, expectedActions, actions)
	assert.Equal(t, expectedMetadata, metadata)
}

// TestValidator_UnmarshalActions_Error verifies error handling when unmarshaling actions fails
func TestValidator_UnmarshalActions_Error(t *testing.T) {
	validator := &Validator{
		backend: &mock.Validator{},
	}

	raw := []byte("some_raw_data")

	mockValidator := validator.backend.(*mock.Validator)
	mockValidator.UnmarshalActionsReturns(nil, errors.New("mocked error"))

	actions, err := validator.UnmarshalActions(raw)

	require.Error(t, err)
	assert.Nil(t, actions)
}

// TestValidator_UnmarshallAndVerify_Error verifies error handling when unmarshaling and verifying fails
func TestValidator_UnmarshallAndVerify_Error(t *testing.T) {
	validator := &Validator{
		backend: &mock.Validator{},
	}
	mockLedger := &mock.ValidatorLedger{}
	raw := []byte("some_raw_data")
	anchor := "some_anchor"

	mockValidator := validator.backend.(*mock.Validator)
	mockValidator.VerifyTokenRequestFromRawReturns(nil, nil, errors.New("mocked error"))
	actions, err := validator.UnmarshallAndVerify(t.Context(), mockLedger, RequestAnchor(anchor), raw)

	require.Error(t, err)
	assert.Nil(t, actions)
}

// TestValidator_UnmarshallAndVerifyWithMetadata_Error verifies error handling when unmarshaling with metadata fails
func TestValidator_UnmarshallAndVerifyWithMetadata_Error(t *testing.T) {
	validator := &Validator{
		backend: &mock.Validator{},
	}
	mockLedger := &mock.ValidatorLedger{}
	raw := []byte("some_raw_data")
	anchor := "some_anchor"

	mockValidator := validator.backend.(*mock.Validator)
	mockValidator.VerifyTokenRequestFromRawReturns(nil, nil, errors.New("mocked error"))
	actions, metadata, err := validator.UnmarshallAndVerifyWithMetadata(t.Context(), mockLedger, RequestAnchor(anchor), raw)
	require.Error(t, err)
	assert.Nil(t, actions)
	assert.Nil(t, metadata)
}

// TestNewValidator verifies Validator constructor initializes backend correctly
func TestNewValidator(t *testing.T) {
	mockBackend := &mock.Validator{}
	validator := NewValidator(mockBackend)

	assert.NotNil(t, validator)
	assert.Equal(t, mockBackend, validator.backend)
}

// TestNewLedgerFromGetter verifies ledger creation from state getter function
func TestNewLedgerFromGetter(t *testing.T) {
	getStateFn := func(id token.ID) ([]byte, error) {
		return []byte("state_data"), nil
	}

	ledger := NewLedgerFromGetter(getStateFn)

	assert.NotNil(t, ledger)
	assert.NotNil(t, ledger.f)
}

// TestStateGetter_GetState verifies state retrieval from ledger
func TestStateGetter_GetState(t *testing.T) {
	expectedData := []byte("state_data")
	getStateFn := func(id token.ID) ([]byte, error) {
		return expectedData, nil
	}

	ledger := NewLedgerFromGetter(getStateFn)
	data, err := ledger.GetState(token.ID{TxId: "tx1", Index: 0})

	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

// TestStateGetter_GetState_Error verifies error handling in state retrieval
func TestStateGetter_GetState_Error(t *testing.T) {
	expectedErr := errors.New("state error")
	getStateFn := func(id token.ID) ([]byte, error) {
		return nil, expectedErr
	}

	ledger := NewLedgerFromGetter(getStateFn)
	data, err := ledger.GetState(token.ID{TxId: "tx1", Index: 0})

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Equal(t, expectedErr, err)
}

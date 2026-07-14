/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token tests actions.go which provides wrappers for token actions (Issue, Transfer).
package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
)

func TestIssueAction_Serialize(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssueAction.SerializeReturns([]byte{1, 2, 3}, nil)
	issueAction := &IssueAction{a: mockIssueAction}
	serialized, err := issueAction.Serialize()
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, serialized)
}

func TestIssueAction_NumOutputs(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssueAction.NumOutputsReturns(5)
	issueAction := &IssueAction{a: mockIssueAction}
	numOutputs := issueAction.NumOutputs()
	assert.Equal(t, 5, numOutputs)
}

func TestIssueAction_GetSerializedOutputs(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockSerializedOutputs := [][]byte{{1, 2}, {3, 4}}
	mockIssueAction.GetSerializedOutputsReturns(mockSerializedOutputs, nil)
	issueAction := &IssueAction{a: mockIssueAction}
	serializedOutputs, err := issueAction.GetSerializedOutputs()
	require.NoError(t, err)
	assert.Equal(t, mockSerializedOutputs, serializedOutputs)
}

func TestIssueAction_IsAnonymous(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssueAction.IsAnonymousReturns(true)
	issueAction := &IssueAction{a: mockIssueAction}
	isAnonymous := issueAction.IsAnonymous()
	assert.True(t, isAnonymous)
}

func TestIssueAction_GetIssuer(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssuer := []byte{1, 2, 3}
	mockIssueAction.GetIssuerReturns(mockIssuer)
	issueAction := &IssueAction{a: mockIssueAction}
	issuer := issueAction.GetIssuer()
	assert.Equal(t, mockIssuer, issuer)
}

func TestIssueAction_GetMetadata(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockMetadata := map[string][]byte{"key1": {1, 2, 3}, "key2": {4, 5, 6}}
	mockIssueAction.GetMetadataReturns(mockMetadata)
	issueAction := &IssueAction{a: mockIssueAction}
	metadata := issueAction.GetMetadata()
	assert.Equal(t, mockMetadata, metadata)
}

func TestTransferAction_Serialize(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockTransferAction.SerializeReturns([]byte{1, 2, 3}, nil)
	transferAction := &TransferAction{mockTransferAction}
	serialized, err := transferAction.Serialize()
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, serialized)
}

func TestTransferAction_NumOutputs(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockTransferAction.NumOutputsReturns(5)
	transferAction := &TransferAction{mockTransferAction}
	numOutputs := transferAction.NumOutputs()
	assert.Equal(t, 5, numOutputs)
}

func TestTransferAction_GetSerializedOutputs(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockSerializedOutputs := [][]byte{{1, 2}, {3, 4}}
	mockTransferAction.GetSerializedOutputsReturns(mockSerializedOutputs, nil)
	transferAction := &TransferAction{mockTransferAction}
	serializedOutputs, err := transferAction.GetSerializedOutputs()
	require.NoError(t, err)
	assert.Equal(t, mockSerializedOutputs, serializedOutputs)
}

func TestTransferAction_IsRedeemAt(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockTransferAction.IsRedeemAtReturns(true)
	transferAction := &TransferAction{mockTransferAction}
	isRedeemAt := transferAction.IsRedeemAt(0)
	assert.True(t, isRedeemAt)
}

func TestTransferAction_SerializeOutputAt(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockSerializedOutput := []byte{1, 2, 3}
	mockTransferAction.SerializeOutputAtReturns(mockSerializedOutput, nil)
	transferAction := &TransferAction{mockTransferAction}
	serializedOutput, err := transferAction.SerializeOutputAt(0)
	require.NoError(t, err)
	assert.Equal(t, mockSerializedOutput, serializedOutput)
}

func TestTransferAction_GetInputs(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockInputs := []*token.ID{{TxId: "input1"}, {TxId: "input2"}}
	mockTransferAction.GetInputsReturns(mockInputs)
	transferAction := &TransferAction{mockTransferAction}
	inputs := transferAction.GetInputs()
	assert.Equal(t, mockInputs, inputs)
}

func TestTransferAction_IsGraphHiding(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockTransferAction.IsGraphHidingReturns(true)
	transferAction := &TransferAction{mockTransferAction}
	isGraphHiding := transferAction.IsGraphHiding()
	assert.True(t, isGraphHiding)
}

// TestIssueAction_IsGraphHiding tests the IsGraphHiding method for IssueAction
func TestIssueAction_IsGraphHiding(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssueAction.IsGraphHidingReturns(true)
	issueAction := &IssueAction{a: mockIssueAction}
	isGraphHiding := issueAction.IsGraphHiding()
	assert.True(t, isGraphHiding)
}

// TestIssueAction_NumInputs tests the NumInputs method for IssueAction
func TestIssueAction_NumInputs(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssueAction.NumInputsReturns(3)
	issueAction := &IssueAction{a: mockIssueAction}
	numInputs := issueAction.NumInputs()
	assert.Equal(t, 3, numInputs)
}

// TestIssueAction_Validate tests the Validate method for IssueAction
func TestIssueAction_Validate(t *testing.T) {
	mockIssueAction := &mock.IssueAction{}
	mockIssueAction.ValidateReturns(nil)
	issueAction := &IssueAction{a: mockIssueAction}
	err := issueAction.Validate()
	require.NoError(t, err)
}

// TestTransferAction_GetSerialNumbers tests the GetSerialNumbers method for TransferAction
func TestTransferAction_GetSerialNumbers(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockSerialNumbers := []string{"serial1", "serial2"}
	mockTransferAction.GetSerialNumbersReturns(mockSerialNumbers)
	transferAction := &TransferAction{mockTransferAction}
	serialNumbers := transferAction.GetSerialNumbers()
	assert.Equal(t, mockSerialNumbers, serialNumbers)
}

// TestTransferAction_Validate tests the Validate method for TransferAction
func TestTransferAction_Validate(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockTransferAction.ValidateReturns(nil)
	transferAction := &TransferAction{mockTransferAction}
	err := transferAction.Validate()
	require.NoError(t, err)
}

// TestTransferAction_GetIssuer tests the GetIssuer method for TransferAction
func TestTransferAction_GetIssuer(t *testing.T) {
	mockTransferAction := &mock.TransferAction{}
	mockIssuer := Identity([]byte{1, 2, 3})
	mockTransferAction.GetIssuerReturns(mockIssuer)
	transferAction := &TransferAction{mockTransferAction}
	issuer := transferAction.GetIssuer()
	assert.Equal(t, mockIssuer, issuer)
}

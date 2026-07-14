/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token_test tests metadata.go which provides token transaction metadata filtering and matching functionality.
package token_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetadata_TestFilterBy tests the FilterBy method of the Metadata struct.
// It verifies that metadata can be correctly filtered based on enrollment IDs.
func TestMetadata_TestFilterBy(t *testing.T) {
	testCases := map[string]func(*testing.T){
		"case0": testFilterByCase0,
		"case1": testFilterByCase1,
	}
	for key, tc := range testCases {
		t.Run(key, tc)
	}
}

// testFilterByCase0 tests filtering metadata for transfer actions.
// It covers various scenarios: filtering by one enrollment ID (sender/receiver), multiple IDs, or an ID that doesn't exist.
func testFilterByCase0(t *testing.T) {
	t.Helper()
	// Case setup:
	// - Two transfers: Alice to Bob, and Charlie to Dave.

	// From Alice to Bob
	aliceToBob := &driver.TransferMetadata{
		Inputs: []*driver.TransferInputMetadata{
			{
				TokenID: &token2.ID{TxId: "pineapple", Index: 0},
				Senders: []*driver.AuditableIdentity{{Identity: token.Identity("Alice"), AuditInfo: []byte("Alice")}},
			},
		},
		Outputs: []*driver.TransferOutputMetadata{
			{
				OutputMetadata: []byte("Bob's output's token metadata"),
				Receivers:      []*driver.AuditableIdentity{{Identity: token.Identity("Bob"), AuditInfo: []byte("Bob")}},
			},
		},
	}
	// From Charlie to Dave
	charlieToDave := &driver.TransferMetadata{
		Inputs: []*driver.TransferInputMetadata{
			{
				TokenID: &token2.ID{TxId: "avocado", Index: 0},
				Senders: []*driver.AuditableIdentity{{Identity: token.Identity("Charlie"), AuditInfo: []byte("Charlie")}},
			},
		},
		Outputs: []*driver.TransferOutputMetadata{
			{
				OutputMetadata: []byte("Dave's output's token metadata"),
				Receivers:      []*driver.AuditableIdentity{{Identity: token.Identity("Dave"), AuditInfo: []byte("Dave")}},
			},
		},
	}

	ws := &mock.WalletService{}
	// Set up sequential returns for GetEnrollmentID to simulate identity resolution for various filter checks.
	ws.GetEnrollmentIDReturnsOnCall(0, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(1, "Charlie", nil)
	ws.GetEnrollmentIDReturnsOnCall(2, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(3, "Charlie", nil)
	ws.GetEnrollmentIDReturnsOnCall(4, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(5, "Charlie", nil)
	ws.GetEnrollmentIDReturnsOnCall(6, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(7, "Charlie", nil)

	metadata := &token.Metadata{
		WalletService: ws,
		TokenRequestMetadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0, TransferMetadata: aliceToBob},
				{ActionID: 1, TransferMetadata: charlieToDave},
			},
			Application: map[string][]byte{"application": []byte("application")},
		},
		Logger: logging.MustGetLogger(),
	}

	// Scenario: Filter by Bob. Bob should only see his received transfer and its sender.
	filteredMetadata, err := metadata.FilterBy(t.Context(), "Bob")
	require.NoError(t, err)
	assert.Len(t, filteredMetadata.TokenRequestMetadata.Actions, 2)
	assertEqualTransferMetadata(t, aliceToBob, filteredMetadata.TokenRequestMetadata.Actions[0].TransferMetadata, true)
	assertEmptyTransferMetadata(t, charlieToDave, filteredMetadata.TokenRequestMetadata.Actions[1].TransferMetadata)

	// Scenario: Filter by Charlie. Charlie should only see his received transfer and its sender.
	filteredMetadata, err = metadata.FilterBy(t.Context(), "Charlie")
	require.NoError(t, err)
	assert.Len(t, filteredMetadata.TokenRequestMetadata.Actions, 2)
	assertEmptyTransferMetadata(t, aliceToBob, filteredMetadata.TokenRequestMetadata.Actions[0].TransferMetadata)
	assertEqualTransferMetadata(t, charlieToDave, filteredMetadata.TokenRequestMetadata.Actions[1].TransferMetadata, true)

	// Scenario: Filter by Eve. Eve should not see any transfer metadata.
	filteredMetadata, err = metadata.FilterBy(t.Context(), "Eve")
	require.NoError(t, err)
	assertEmptyTransferMetadata(t, aliceToBob, filteredMetadata.TokenRequestMetadata.Actions[0].TransferMetadata)
	assertEmptyTransferMetadata(t, charlieToDave, filteredMetadata.TokenRequestMetadata.Actions[1].TransferMetadata)

	// Scenario: Filter by both Bob and Charlie. Both should see their respective metadata.
	filteredMetadata, err = metadata.FilterBy(t.Context(), "Bob", "Charlie")
	require.NoError(t, err)
	assertEqualTransferMetadata(t, aliceToBob, filteredMetadata.TokenRequestMetadata.Actions[0].TransferMetadata, true)
	assertEqualTransferMetadata(t, charlieToDave, filteredMetadata.TokenRequestMetadata.Actions[1].TransferMetadata, true)

	// Scenario: No enrollment IDs provided. Should return the original metadata unchanged.
	filteredMetadata, err = metadata.FilterBy(t.Context())
	require.NoError(t, err)
	assertEqualTransferMetadata(t, aliceToBob, filteredMetadata.TokenRequestMetadata.Actions[0].TransferMetadata, false)
	assertEqualTransferMetadata(t, charlieToDave, filteredMetadata.TokenRequestMetadata.Actions[1].TransferMetadata, false)
}

// testFilterByCase1 tests filtering metadata for issue actions.
func testFilterByCase1(t *testing.T) {
	t.Helper()
	// Case setup: Two separate issues for Alice and Bob.

	// Alice's issue
	aliceIssue := &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{Identity: token.Identity("Issuer"), AuditInfo: []byte("Issuer Audit Info")},
		Outputs: []*driver.IssueOutputMetadata{
			{
				OutputMetadata: []byte("Alice's output's token info"),
				Receivers:      []*driver.AuditableIdentity{{Identity: token.Identity("Alice"), AuditInfo: []byte("Alice")}},
			},
		},
	}
	// Bob's issue
	bobIssue := &driver.IssueMetadata{
		Issuer: driver.AuditableIdentity{Identity: token.Identity("Issuer"), AuditInfo: []byte("Issuer Audit Info")},
		Outputs: []*driver.IssueOutputMetadata{
			{
				OutputMetadata: []byte("Bob's output's token info"),
				Receivers:      []*driver.AuditableIdentity{{Identity: token.Identity("Bob"), AuditInfo: []byte("Bob")}},
			},
		},
	}

	ws := &mock.WalletService{}
	ws.GetEnrollmentIDReturnsOnCall(0, "Alice", nil)
	ws.GetEnrollmentIDReturnsOnCall(1, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(2, "Alice", nil)
	ws.GetEnrollmentIDReturnsOnCall(3, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(4, "Alice", nil)
	ws.GetEnrollmentIDReturnsOnCall(5, "Bob", nil)
	ws.GetEnrollmentIDReturnsOnCall(6, "Alice", nil)
	ws.GetEnrollmentIDReturnsOnCall(7, "Bob", nil)

	metadata := &token.Metadata{
		WalletService: ws,
		TokenRequestMetadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0, IssueMetadata: aliceIssue},
				{ActionID: 1, IssueMetadata: bobIssue},
			},
		},
		Logger: logging.MustGetLogger(),
	}

	// Filter by Alice. Only Alice's issue should be present.
	filteredMetadata, err := metadata.FilterBy(t.Context(), "Alice")
	require.NoError(t, err)
	assertEqualIssueMetadata(t, aliceIssue, filteredMetadata.TokenRequestMetadata.Actions[0].IssueMetadata)
	assertEmptyIssueMetadata(t, bobIssue, filteredMetadata.TokenRequestMetadata.Actions[1].IssueMetadata)

	// Filter by Bob. Only Bob's issue should be present.
	filteredMetadata, err = metadata.FilterBy(t.Context(), "Bob")
	require.NoError(t, err)
	assertEmptyIssueMetadata(t, aliceIssue, filteredMetadata.TokenRequestMetadata.Actions[0].IssueMetadata)
	assertEqualIssueMetadata(t, bobIssue, filteredMetadata.TokenRequestMetadata.Actions[1].IssueMetadata)
}

// assertEqualIssueMetadata is a helper to verify that issue metadata has been correctly preserved after filtering.
func assertEqualIssueMetadata(t *testing.T, original, filtered *driver.IssueMetadata) {
	t.Helper()
	assert.Equal(t, original, filtered)
}

// assertEmptyIssueMetadata verifies that metadata not belonging to a party has been cleared.
func assertEmptyIssueMetadata(t *testing.T, original, filtered *driver.IssueMetadata) {
	t.Helper()
	assert.Equal(t, original.Issuer, filtered.Issuer)
	assert.Len(t, original.Outputs, len(filtered.Outputs))
	for i := range len(original.Outputs) {
		assert.Empty(t, filtered.Outputs[i])
	}
}

// assertEmptyTransferMetadata verifies that transfer metadata not belonging to a party has been cleared.
func assertEmptyTransferMetadata(t *testing.T, original, filtered *driver.TransferMetadata) {
	t.Helper()
	for _, input := range filtered.Inputs {
		assert.Nil(t, input.TokenID)
		assert.Nil(t, input.Senders)
	}
	assert.Len(t, original.Outputs, len(filtered.Outputs))
	for i := range len(original.Outputs) {
		assert.Empty(t, filtered.Outputs[i])
	}
}

// assertEqualTransferMetadata verifies that transfer metadata has been correctly preserved or partially filtered.
func assertEqualTransferMetadata(t *testing.T, original, filtered *driver.TransferMetadata, noInputs bool) {
	t.Helper()
	for i, input := range original.Inputs {
		if noInputs {
			assert.Nil(t, filtered.Inputs[i].TokenID)
		} else {
			assert.Equal(t, input.TokenID, filtered.Inputs[i].TokenID)
		}
		assert.Equal(t, input.Senders, filtered.Inputs[i].Senders)
	}
	assert.Equal(t, original.Outputs, filtered.Outputs)
	assert.Equal(t, original.ExtraSigners, filtered.ExtraSigners)
}

// TestMetadata_TestMatchTransferAction tests that TransferMetadata.Match correctly validates a TransferAction.
func TestMetadata_TestMatchTransferAction(t *testing.T) {
	mockIssuer := token.Identity("issuer1")
	signer1 := token.Identity("signer1")

	transferActionWithIssuer := &mock.TransferAction{}
	transferActionWithIssuer.GetIssuerReturns(mockIssuer)
	transferActionWithIssuer.NumInputsReturns(1)
	transferActionWithIssuer.NumOutputsReturns(1)
	transferActionWithIssuer.ExtraSignersReturns([]token.Identity{signer1})

	tests := []struct {
		name          string
		action        *token.TransferAction
		meta          *token.TransferMetadata
		wantErr       bool
		expectedError string
	}{
		{
			name:   "match",
			action: &token.TransferAction{transferActionWithIssuer},
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{
					Issuer: driver.AuditableIdentity{
						Identity: mockIssuer,
					},
					Inputs:  []*driver.TransferInputMetadata{{}},
					Outputs: []*driver.TransferOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{
						{Identity: signer1},
					},
				},
			},
			wantErr: false,
		},
		{
			name:   "nil action",
			action: nil,
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{},
			},
			wantErr:       true,
			expectedError: "nil transfer action",
		},
		{
			name: "validation error",
			action: func() *token.TransferAction {
				a := &mock.TransferAction{}
				a.ValidateReturns(errors.New("validation failed"))

				return &token.TransferAction{a}
			}(),
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{},
			},
			wantErr:       true,
			expectedError: "failed validating transfer action",
		},
		{
			name:   "mismatch inputs",
			action: &token.TransferAction{transferActionWithIssuer},
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{
					Inputs: []*driver.TransferInputMetadata{},
				},
			},
			wantErr:       true,
			expectedError: "expected [0] inputs but got [1]",
		},
		{
			name:   "mismatch outputs",
			action: &token.TransferAction{transferActionWithIssuer},
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{
					Inputs:  []*driver.TransferInputMetadata{{}},
					Outputs: []*driver.TransferOutputMetadata{},
				},
			},
			wantErr:       true,
			expectedError: "expected [0] outputs but got [1]",
		},
		{
			name:   "mismatch extra signers count",
			action: &token.TransferAction{transferActionWithIssuer},
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{
					Inputs:       []*driver.TransferInputMetadata{{}},
					Outputs:      []*driver.TransferOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{},
				},
			},
			wantErr:       true,
			expectedError: "expected [0] extra signers but got [1]",
		},
		{
			name:   "mismatch extra signers",
			action: &token.TransferAction{transferActionWithIssuer},
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{
					Inputs:  []*driver.TransferInputMetadata{{}},
					Outputs: []*driver.TransferOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{
						{Identity: token.Identity("other")},
					},
				},
			},
			wantErr:       true,
			expectedError: "expected extra signer [2SmKENGwc1g33EvYXaxkGw887yekfl1TpU8vP1svz/o=] but got [kjnCsbFKyPwMtSk64mTokc83jkySwm75ZY82AM4yLrY=]",
		},
		{
			name:   "error: mismatch issuer",
			action: &token.TransferAction{transferActionWithIssuer},
			meta: &token.TransferMetadata{
				&driver.TransferMetadata{
					Issuer: driver.AuditableIdentity{
						Identity: token.Identity("other"),
					},
					Inputs:  []*driver.TransferInputMetadata{{}},
					Outputs: []*driver.TransferOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{
						{Identity: signer1},
					},
				},
			},
			wantErr:       true,
			expectedError: "expected issuer [2SmKENGwc1g33EvYXaxkGw887yekfl1TpU8vP1svz/o=] but got [issuer1]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.meta.Match(tt.action)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestMetadata_SpentTokenID tests the SpentTokenID function.
func TestMetadata_SpentTokenID(t *testing.T) {
	issueTokenID := &token2.ID{TxId: "issueTx", Index: 0}
	transferTokenID := &token2.ID{TxId: "transferTx", Index: 1}

	metadata := &token.Metadata{
		TokenRequestMetadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					IssueMetadata: &driver.IssueMetadata{
						Inputs: []*driver.IssueInputMetadata{{TokenID: issueTokenID}},
					},
				},
				{
					ActionID: 1,
					TransferMetadata: &driver.TransferMetadata{
						Inputs: []*driver.TransferInputMetadata{{TokenID: transferTokenID}},
					},
				},
			},
		},
	}

	spentIDs := metadata.SpentTokenID()
	assert.Len(t, spentIDs, 2)
	assert.Contains(t, spentIDs, issueTokenID)
	assert.Contains(t, spentIDs, transferTokenID)
}

// TestMetadata_Issue_Transfer tests the Issue and Transfer retrieval functions of Metadata.
func TestMetadata_Issue_Transfer(t *testing.T) {
	issueMeta := &driver.IssueMetadata{}
	transferMeta := &driver.TransferMetadata{}
	metadata := &token.Metadata{
		TokenRequestMetadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{ActionID: 0, IssueMetadata: issueMeta},
				{ActionID: 1, TransferMetadata: transferMeta},
			},
		},
	}

	// Test retrieving the first issue metadata.
	issue, err := metadata.Issue(0)
	require.NoError(t, err)
	assert.Equal(t, issueMeta, issue.IssueMetadata)

	// Test out-of-bounds issue index.
	_, err = metadata.Issue(1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index [1] out of range [0:1]")

	// Test retrieving the first transfer metadata.
	transfer, err := metadata.Transfer(0)
	require.NoError(t, err)
	assert.Equal(t, transferMeta, transfer.TransferMetadata)

	// Test out-of-bounds transfer index.
	_, err = metadata.Transfer(1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index [1] out of range [0:1]")
}

// TestIssueMetadata_IsOutputAbsent verifies the output presence check for IssueMetadata.
func TestIssueMetadata_IsOutputAbsent(t *testing.T) {
	issueMeta := &token.IssueMetadata{
		IssueMetadata: &driver.IssueMetadata{
			Outputs: []*driver.IssueOutputMetadata{
				{OutputMetadata: []byte("meta")},
				nil,
			},
		},
	}

	assert.False(t, issueMeta.IsOutputAbsent(0))
	assert.True(t, issueMeta.IsOutputAbsent(1))
	assert.True(t, issueMeta.IsOutputAbsent(2))
	assert.True(t, issueMeta.IsOutputAbsent(-1))
}

// TestTransferMetadata_IsOutputInputAbsent verifies output and input presence checks for TransferMetadata.
func TestTransferMetadata_IsOutputInputAbsent(t *testing.T) {
	transferMeta := &token.TransferMetadata{
		TransferMetadata: &driver.TransferMetadata{
			Inputs: []*driver.TransferInputMetadata{
				{Senders: []*driver.AuditableIdentity{{Identity: []byte("sender")}}},
				{Senders: nil},
				nil,
			},
			Outputs: []*driver.TransferOutputMetadata{
				{OutputMetadata: []byte("meta")},
				nil,
			},
		},
	}

	// Test outputs presence.
	assert.False(t, transferMeta.IsOutputAbsent(0))
	assert.True(t, transferMeta.IsOutputAbsent(1))
	assert.True(t, transferMeta.IsOutputAbsent(2))

	// Test inputs presence.
	assert.False(t, transferMeta.IsInputAbsent(0))
	assert.True(t, transferMeta.IsInputAbsent(1))
	assert.True(t, transferMeta.IsInputAbsent(2))
	assert.True(t, transferMeta.IsInputAbsent(3))
}

// TestMetadata_FilterBy_Errors tests error propagation in the FilterBy method.
func TestMetadata_FilterBy_Errors(t *testing.T) {
	ctx := context.Background()
	ws := &mock.WalletService{}
	ws.GetEnrollmentIDReturns("", errors.New("failed getting enrollment ID"))

	metadata := &token.Metadata{
		WalletService: ws,
		Logger:        logging.MustGetLogger(),
		TokenRequestMetadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					IssueMetadata: &driver.IssueMetadata{
						Outputs: []*driver.IssueOutputMetadata{
							{Receivers: []*driver.AuditableIdentity{{Identity: []byte("receiver")}}},
						},
					},
				},
			},
		},
	}

	// Scenario: WalletService fails to resolve enrollment ID during issue filtering.
	_, err := metadata.FilterBy(ctx, "Bob")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed filtering issue")

	// Scenario: WalletService fails to resolve enrollment ID during transfer filtering.
	metadata.TokenRequestMetadata.Actions = []*driver.ActionMetadataEntry{
		{
			ActionID: 0,
			TransferMetadata: &driver.TransferMetadata{
				Outputs: []*driver.TransferOutputMetadata{
					{Receivers: []*driver.AuditableIdentity{{Identity: []byte("receiver")}}},
				},
			},
		},
	}
	_, err = metadata.FilterBy(ctx, "Bob")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed filtering transfer")
}

// TestIssueMetadata_Match tests that IssueMetadata.Match correctly validates an IssueAction.
func TestIssueMetadata_Match(t *testing.T) {
	issuer1 := token.Identity("issuer1")
	issuer2 := token.Identity("issuer2")
	signer1 := token.Identity("signer1")

	action := &mock.IssueAction{}
	action.GetIssuerReturns(issuer1)
	action.NumInputsReturns(1)
	action.NumOutputsReturns(1)
	action.ExtraSignersReturns([]token.Identity{signer1})

	tests := []struct {
		name          string
		meta          *token.IssueMetadata
		action        *token.IssueAction
		wantErr       bool
		expectedError string
	}{
		{
			name: "match",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{
					Issuer:  driver.AuditableIdentity{Identity: issuer1},
					Inputs:  []*driver.IssueInputMetadata{{}},
					Outputs: []*driver.IssueOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{
						{Identity: signer1},
					},
				},
			},
			action:  token.NewIssueAction(action),
			wantErr: false,
		},
		{
			name: "nil action",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{},
			},
			action:        nil,
			wantErr:       true,
			expectedError: "nil issue action",
		},
		{
			name: "validation error",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{},
			},
			action: func() *token.IssueAction {
				a := &mock.IssueAction{}
				a.ValidateReturns(errors.New("validation failed"))

				return token.NewIssueAction(a)
			}(),
			wantErr:       true,
			expectedError: "failed validating issue action",
		},
		{
			name: "mismatch inputs",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{
					Inputs: []*driver.IssueInputMetadata{},
				},
			},
			action:        token.NewIssueAction(action),
			wantErr:       true,
			expectedError: "expected [0] inputs but got [1]",
		},
		{
			name: "mismatch outputs",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{
					Inputs:  []*driver.IssueInputMetadata{{}},
					Outputs: []*driver.IssueOutputMetadata{},
				},
			},
			action:        token.NewIssueAction(action),
			wantErr:       true,
			expectedError: "expected [0] outputs but got [1]",
		},
		{
			name: "mismatch extra signers count",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{
					Inputs:       []*driver.IssueInputMetadata{{}},
					Outputs:      []*driver.IssueOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{},
				},
			},
			action:        token.NewIssueAction(action),
			wantErr:       true,
			expectedError: "expected [1] extra signers but got [0]",
		},
		{
			name: "mismatch extra signers",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{
					Inputs:  []*driver.IssueInputMetadata{{}},
					Outputs: []*driver.IssueOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{
						{Identity: token.Identity("other")},
					},
				},
			},
			action:        token.NewIssueAction(action),
			wantErr:       true,
			expectedError: "extra signer [kjnCsbFKyPwMtSk64mTokc83jkySwm75ZY82AM4yLrY=] from action not found in metadata",
		},
		{
			name: "mismatch issuer",
			meta: &token.IssueMetadata{
				IssueMetadata: &driver.IssueMetadata{
					Issuer:  driver.AuditableIdentity{Identity: issuer2},
					Inputs:  []*driver.IssueInputMetadata{{}},
					Outputs: []*driver.IssueOutputMetadata{{}},
					ExtraSigners: []driver.AuditableIdentity{
						{Identity: signer1},
					},
				},
			},
			action:        token.NewIssueAction(action),
			wantErr:       true,
			expectedError: "expected issuer [rsmHNvYoUlo66pbKvtlmcSCSUva4NmJtEX1q+N9m+hk=] but got [issuer1]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.meta.Match(tt.action)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

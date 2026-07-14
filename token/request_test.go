/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token tests Request serialization and application metadata handling.
package token

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	driver2 "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummySelector struct{}

func (d dummySelector) Select(ctx context.Context, ownerFilter OwnerFilter, q string, tokenType token.Type) ([]*token.ID, token.Quantity, error) {
	panic("implement me")
}

func (d dummySelector) Close() error {
	panic("implement me")
}

// TestRequestSerialization verifies that a Request can be serialized to bytes and
// deserialized back without data loss. Tests both full serialization (Bytes/FromBytes)
// and audit-specific serialization (MarshalToAudit).
func TestRequestSerialization(t *testing.T) {
	r := NewRequest(nil, "hello world")
	r.Actions = &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Signatures: []*driver.RequestSignature{
			{
				Action: &driver.ActionSignature{
					ActionID:  0,
					Signature: []byte("signature1"),
				},
			},
			{
				Auditor: &driver.AuditorSignature{
					Identity:  Identity("auditor1"),
					Signature: []byte("signature1"),
				},
			},
		},
	}
	raw, err := r.Bytes()
	require.NoError(t, err)

	r2 := NewRequest(nil, "")
	err = r2.FromBytes(raw)
	require.NoError(t, err)
	raw2, err := r2.Bytes()
	require.NoError(t, err)

	assert.Equal(t, raw, raw2)

	mRaw, err := r.MarshalToAudit()
	require.NoError(t, err)
	mRaw2, err := r2.MarshalToAudit()
	require.NoError(t, err)

	assert.Equal(t, mRaw, mRaw2)
}

// TestRequest_FromBytes_ErrorCases tests error paths in FromBytes method
func TestRequest_FromBytes_ErrorCases(t *testing.T) {
	t.Run("invalid bytes", func(t *testing.T) {
		r := &Request{}
		err := r.FromBytes([]byte("invalid data"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshaling request")
	})

	t.Run("empty bytes", func(t *testing.T) {
		r := &Request{}
		err := r.FromBytes([]byte{})
		require.Error(t, err)
	})
}

// TestRequest_ApplicationMetadata verifies that ApplicationMetadata correctly retrieves
// metadata values by key, returning nil for non-existent keys.
func TestRequest_ApplicationMetadata(t *testing.T) {
	// Test case: No application metadata set
	request := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Application: map[string][]byte{},
		},
	}

	// Retrieve non-existent metadata
	data := request.ApplicationMetadata("key")
	assert.Nil(t, data)

	// Test case: Application metadata set
	request = &Request{
		Metadata: &driver.TokenRequestMetadata{
			Application: map[string][]byte{
				"key1": []byte("value1"),
				"key2": []byte("value2"),
			},
		},
	}

	// Retrieve existing metadata
	data = request.ApplicationMetadata("key1")
	assert.Equal(t, []byte("value1"), data)

	// Retrieve non-existent metadata
	data = request.ApplicationMetadata("non_existent_key")
	assert.Nil(t, data)
}

// TestRequest_SetApplicationMetadata verifies that SetApplicationMetadata correctly
// stores metadata values, initializing the metadata structure if needed.
func TestRequest_SetApplicationMetadata(t *testing.T) {
	// Test case: No application metadata set
	request := &Request{}

	// Set application metadata
	request.SetApplicationMetadata("key", []byte("value"))

	// Assert metadata set correctly
	assert.NotNil(t, request.Metadata)
	assert.NotNil(t, request.Metadata.Application)
	assert.Equal(t, []byte("value"), request.Metadata.Application["key"])

	// Test case: Application metadata already set
	request = &Request{
		Metadata: &driver.TokenRequestMetadata{
			Application: map[string][]byte{
				"key1": []byte("value1"),
			},
		},
	}

	// Set additional application metadata
	request.SetApplicationMetadata("key2", []byte("value2"))

	// Assert metadata set correctly
	assert.NotNil(t, request.Metadata)
	assert.NotNil(t, request.Metadata.Application)
	assert.Equal(t, []byte("value1"), request.Metadata.Application["key1"])
	assert.Equal(t, []byte("value2"), request.Metadata.Application["key2"])
}

// TestCompileIssueOptions tests the compileIssueOptions function
func TestCompileIssueOptions(t *testing.T) {
	t.Run("no options", func(t *testing.T) {
		opts, err := compileIssueOptions()
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Nil(t, opts.Attributes)
	})

	t.Run("with issue attribute", func(t *testing.T) {
		opts, err := compileIssueOptions(
			WithIssueAttribute("key1", "value1"),
			WithIssueAttribute("key2", 123),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, "value1", opts.Attributes["key1"])
		assert.Equal(t, 123, opts.Attributes["key2"])
	})

	t.Run("with issue metadata", func(t *testing.T) {
		metadata := []byte("test metadata")
		opts, err := compileIssueOptions(
			WithIssueMetadata("meta1", metadata),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, metadata, opts.Attributes[IssueMetadataPrefix+"meta1"])
	})

	t.Run("with error in option", func(t *testing.T) {
		errorOption := func(o *IssueOptions) error {
			return errors.New("test error")
		}
		_, err := compileIssueOptions(errorOption)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test error")
	})
}

// TestCompileTransferOptions tests the CompileTransferOptions function
func TestCompileTransferOptions(t *testing.T) {
	t.Run("no options", func(t *testing.T) {
		opts, err := CompileTransferOptions()
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Nil(t, opts.Attributes)
		assert.Nil(t, opts.Selector)
		assert.Nil(t, opts.TokenIDs)
	})

	t.Run("with transfer attribute", func(t *testing.T) {
		opts, err := CompileTransferOptions(
			WithTransferAttribute("key1", "value1"),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, "value1", opts.Attributes["key1"])
	})

	t.Run("with transfer metadata", func(t *testing.T) {
		metadata := []byte("transfer metadata")
		opts, err := CompileTransferOptions(
			WithTransferMetadata("meta1", metadata),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, metadata, opts.Attributes[TransferMetadataPrefix+"meta1"])
	})

	t.Run("with public transfer metadata", func(t *testing.T) {
		metadata := []byte("public metadata")
		opts, err := CompileTransferOptions(
			WithPublicTransferMetadata("public1", metadata),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, metadata, opts.Attributes[TransferMetadataPrefix+PublicMetadataPrefix+"public1"])
	})

	t.Run("with public issue metadata", func(t *testing.T) {
		metadata := []byte("public issue metadata")
		opts, err := compileIssueOptions(
			WithPublicIssueMetadata("public1", metadata),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, metadata, opts.Attributes[IssueMetadataPrefix+PublicMetadataPrefix+"public1"])
	})

	t.Run("with token selector", func(t *testing.T) {
		mockSelector := &dummySelector{}
		opts, err := CompileTransferOptions(
			WithTokenSelector(mockSelector),
		)
		require.NoError(t, err)
		assert.NotNil(t, opts)
		assert.Equal(t, mockSelector, opts.Selector)
	})

	t.Run("with error in option", func(t *testing.T) {
		errorOption := func(o *TransferOptions) error {
			return errors.New("test transfer error")
		}
		_, err := CompileTransferOptions(errorOption)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test transfer error")
	})
}

// TestWithTokenIDs tests the WithTokenIDs option
func TestWithTokenIDs(t *testing.T) {
	tokenID1 := &token.ID{TxId: "tx1", Index: 0}
	tokenID2 := &token.ID{TxId: "tx2", Index: 1}

	opts, err := CompileTransferOptions(
		WithTokenIDs(tokenID1, tokenID2),
	)
	require.NoError(t, err)
	assert.NotNil(t, opts)
	assert.Len(t, opts.TokenIDs, 2)
	assert.Equal(t, tokenID1, opts.TokenIDs[0])
	assert.Equal(t, tokenID2, opts.TokenIDs[1])
}

// TestWithRestRecipientIdentity tests the WithRestRecipientIdentity option
func TestWithRestRecipientIdentity(t *testing.T) {
	recipientData := &RecipientData{
		Identity: []byte("recipient identity"),
	}

	opts, err := CompileTransferOptions(
		WithRestRecipientIdentity(recipientData),
	)
	require.NoError(t, err)
	assert.NotNil(t, opts)
	assert.Equal(t, recipientData, opts.RestRecipientIdentity)
}

// TestRequest_ID tests the ID method
func TestRequest_ID(t *testing.T) {
	r := &Request{
		Anchor: "test-anchor-123",
	}
	assert.Equal(t, RequestAnchor("test-anchor-123"), r.ID())
}

// TestRequest_String tests the String method
func TestRequest_String(t *testing.T) {
	r := &Request{
		Anchor: "test-anchor",
	}
	assert.Equal(t, "test-anchor", r.String())
}

// TestRequest_SetTokenService tests SetTokenService
func TestRequest_SetTokenService(t *testing.T) {
	r := &Request{}
	tms := &ManagementService{
		id: TMSID{Network: "test"},
	}

	r.SetTokenService(tms)
	assert.Equal(t, tms, r.TokenService)
}

// TestRequest_AddAuditorSignature tests AddAuditorSignature
func TestRequest_AddAuditorSignature(t *testing.T) {
	r := &Request{
		Actions: &driver.TokenRequest{},
	}

	identity := Identity("auditor1")
	signature := []byte("signature1")

	r.AddAuditorSignature(identity, signature)

	require.Len(t, r.Actions.Signatures, 1)
	require.NotNil(t, r.Actions.Signatures[0].Auditor)
	assert.Equal(t, identity, r.Actions.Signatures[0].Auditor.Identity)
	assert.Equal(t, signature, r.Actions.Signatures[0].Auditor.Signature)

	// Add another
	r.AddAuditorSignature(Identity("auditor2"), []byte("sig2"))
	require.Len(t, r.Actions.Signatures, 2)
	require.NotNil(t, r.Actions.Signatures[1].Auditor)
}

// TestRequest_AllApplicationMetadata tests AllApplicationMetadata
func TestRequest_AllApplicationMetadata(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Application: map[string][]byte{
				"key1": []byte("value1"),
				"key2": []byte("value2"),
			},
		},
	}

	metadata := r.AllApplicationMetadata()
	assert.Len(t, metadata, 2)
	assert.Equal(t, []byte("value1"), metadata["key1"])
	assert.Equal(t, []byte("value2"), metadata["key2"])
}

// TestRequest_MarshalToSign tests MarshalToSign
func TestRequest_MarshalToSign(t *testing.T) {
	r := &Request{
		Anchor: "test-anchor",
		Actions: &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		},
	}

	data, err := r.MarshalToSign()
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.NotEmpty(t, data)
}

// TestRequest_RequestToBytes tests RequestToBytes
func TestRequest_RequestToBytes(t *testing.T) {
	r := &Request{
		Anchor: "test-anchor",
		Actions: &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		},
	}

	data, err := r.RequestToBytes()
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.NotEmpty(t, data)
}

// TestRequest_Issues tests Issues method with empty metadata
func TestRequest_Issues_Empty(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{},
		},
	}

	issues := r.Issues()
	assert.Empty(t, issues)
}

// TestRequest_Transfers tests Transfers method with empty metadata
func TestRequest_Transfers_Empty(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{},
		},
	}

	transfers := r.Transfers()
	assert.Empty(t, transfers)
}

// TestRequest_TransferSigners tests TransferSigners with empty metadata
func TestRequest_TransferSigners_Empty(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{},
		},
	}

	signers := r.TransferSigners()
	assert.Empty(t, signers)
}

// TestRequest_IssueSigners tests IssueSigners with empty metadata
func TestRequest_IssueSigners_Empty(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{},
		},
	}

	signers := r.IssueSigners()
	assert.Empty(t, signers)
}

// TestNewRequestFromBytes tests NewRequestFromBytes function
func TestNewRequestFromBytes(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		// Create a request and serialize it
		original := NewRequest(nil, "test-anchor")
		original.Actions = &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		}

		actionsBytes, err := original.Actions.Bytes()
		require.NoError(t, err)

		metadataBytes, err := original.Metadata.Bytes()
		require.NoError(t, err)

		// Deserialize using NewRequestFromBytes
		restored, err := NewRequestFromBytes(nil, "test-anchor", actionsBytes, metadataBytes)
		require.NoError(t, err)
		assert.NotNil(t, restored)
		assert.Equal(t, RequestAnchor("test-anchor"), restored.Anchor)
		assert.NotNil(t, restored.Actions)
		assert.NotNil(t, restored.Metadata)
	})

	t.Run("empty metadata", func(t *testing.T) {
		original := NewRequest(nil, "test-anchor")
		original.Actions = &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		}

		actionsBytes, err := original.Actions.Bytes()
		require.NoError(t, err)

		// Deserialize with empty metadata
		restored, err := NewRequestFromBytes(nil, "test-anchor", actionsBytes, nil)
		require.NoError(t, err)
		assert.NotNil(t, restored)
		assert.Equal(t, RequestAnchor("test-anchor"), restored.Anchor)
	})

	t.Run("invalid actions", func(t *testing.T) {
		_, err := NewRequestFromBytes(nil, "test-anchor", []byte("invalid"), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshalling token request")
	})

	t.Run("invalid metadata", func(t *testing.T) {
		original := NewRequest(nil, "test-anchor")
		original.Actions = &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		}

		actionsBytes, err := original.Actions.Bytes()
		require.NoError(t, err)

		_, err = NewRequestFromBytes(nil, "test-anchor", actionsBytes, []byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed unmarshalling token request metadata")
	})
}

// TestNewFullRequestFromBytes tests NewFullRequestFromBytes function
func TestNewFullRequestFromBytes(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		// Create and serialize a full request
		original := NewRequest(nil, "test-anchor")
		original.Actions = &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			},
		}

		fullBytes, err := original.Bytes()
		require.NoError(t, err)

		// Deserialize using NewFullRequestFromBytes
		restored, err := NewFullRequestFromBytes(nil, fullBytes)
		require.NoError(t, err)
		assert.NotNil(t, restored)
		assert.Equal(t, original.Anchor, restored.Anchor)
	})

	t.Run("invalid bytes", func(t *testing.T) {
		_, err := NewFullRequestFromBytes(nil, []byte("invalid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal request")
	})
}

// TestRequest_Issue tests the Issue function
func TestRequest_Issue(t *testing.T) {
	ctx := t.Context()

	t.Run("nil wallet", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		_, err := req.Issue(ctx, nil, Identity("receiver"), "USD", 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wallet is nil")
	})

	t.Run("empty type", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		wallet := &IssuerWallet{}
		_, err := req.Issue(ctx, wallet, Identity("receiver"), "", 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "type is empty")
	})

	t.Run("zero quantity", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		wallet := &IssuerWallet{}
		_, err := req.Issue(ctx, wallet, Identity("receiver"), "USD", 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "q is zero")
	})

	t.Run("none receiver", func(t *testing.T) {
		// Setup mocks
		mockPP := &driver2.PublicParameters{}
		mockPP.MaxTokenValueReturns(1000000)

		mockPPM := &driver2.PublicParamsManager{}
		mockPPM.PublicParametersReturns(mockPP)

		tms := &ManagementService{
			publicParametersManager: &PublicParametersManager{
				ppm: mockPPM,
				pp:  &PublicParameters{PublicParameters: mockPP},
			},
		}
		req := NewRequest(tms, "test-anchor")

		mockWallet := &IssuerWallet{}
		_, err := req.Issue(ctx, mockWallet, Identity(nil), "USD", 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "all recipients should be defined")
	})

	t.Run("quantity exceeds max token value", func(t *testing.T) {
		// Setup mocks
		mockPP := &driver2.PublicParameters{}
		mockPP.MaxTokenValueReturns(100)

		mockPPM := &driver2.PublicParamsManager{}
		mockPPM.PublicParametersReturns(mockPP)

		tms := &ManagementService{
			publicParametersManager: &PublicParametersManager{
				ppm: mockPPM,
				pp:  &PublicParameters{PublicParameters: mockPP},
			},
		}
		req := NewRequest(tms, "test-anchor")

		mockWallet := &IssuerWallet{}
		_, err := req.Issue(ctx, mockWallet, Identity("receiver"), "USD", 200)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "q is larger than max token value")
	})
}

// TestRequest_Transfer tests the Transfer function
func TestRequest_Transfer(t *testing.T) {
	ctx := t.Context()

	t.Run("zero value", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		wallet := &OwnerWallet{}
		_, err := req.Transfer(ctx, wallet, "USD", []uint64{0, 100}, []Identity{Identity("receiver1"), Identity("receiver2")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is zero")
	})

	t.Run("multiple zero values", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		wallet := &OwnerWallet{}
		_, err := req.Transfer(ctx, wallet, "USD", []uint64{100, 0}, []Identity{Identity("receiver1"), Identity("receiver2")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "value is zero")
	})
}

// TestRequest_Redeem tests the Redeem function
func TestRequest_Redeem(t *testing.T) {
	// Redeem internally calls prepareTransfer which validates zero values
	// The validation happens in the Transfer path, so we test it there
	// Redeem-specific logic would require extensive mocking of TransferService
	t.Skip("Redeem requires extensive mocking - covered by integration tests")
}

// TestRequest_Upgrade tests the Upgrade function
func TestRequest_Upgrade(t *testing.T) {
	ctx := t.Context()

	t.Run("nil wallet", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		_, err := req.Upgrade(ctx, nil, Identity("receiver"), nil, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wallet is nil")
	})

	t.Run("empty tokens", func(t *testing.T) {
		req := NewRequest(nil, "test-anchor")
		wallet := &IssuerWallet{}
		_, err := req.Upgrade(ctx, wallet, Identity("receiver"), nil, []token.LedgerToken{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tokens is empty")
	})
}

// TestRequest_IsValid tests the IsValid function
func TestRequest_IsValid(t *testing.T) {
	ctx := t.Context()

	t.Run("nil token service", func(t *testing.T) {
		req := &Request{
			TokenService: nil,
			Actions:      &driver.TokenRequest{},
			Metadata:     &driver.TokenRequestMetadata{},
		}
		err := req.IsValid(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid token service in request")
	})

	t.Run("nil actions", func(t *testing.T) {
		req := &Request{
			TokenService: &ManagementService{},
			Actions:      nil,
			Metadata:     &driver.TokenRequestMetadata{},
		}
		err := req.IsValid(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid actions in request")
	})

	t.Run("nil metadata", func(t *testing.T) {
		req := &Request{
			TokenService: &ManagementService{},
			Actions:      &driver.TokenRequest{},
			Metadata:     nil,
		}
		err := req.IsValid(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid metadata in request")
	})
}

// TestRequest_Outputs tests the Outputs function
func TestRequest_Outputs(t *testing.T) {
	// Outputs requires extensive mocking of IssueService, TransferService, and metadata
	// The function is better covered by integration tests
	t.Skip("Outputs requires extensive mocking - covered by integration tests")
}

// Note: Counterfeiter-generated mocks are used from driver/mock package:
// - TokenManagerService: driver/mock/tms.go
// - IssueAction: driver/mock/ia.go

// TestRequest_Issues tests Issues method with populated metadata
func TestRequest_Issues(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					IssueMetadata: &driver.IssueMetadata{
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer1"),
						},
						Outputs: []*driver.IssueOutputMetadata{
							{
								Receivers: []*driver.AuditableIdentity{
									{Identity: Identity("recipient1")},
								},
							},
						},
						ExtraSigners: []driver.AuditableIdentity{
							{Identity: Identity("signer1")},
						},
					},
				},
				{
					ActionID: 1,
					IssueMetadata: &driver.IssueMetadata{
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer2"),
						},
						Outputs: []*driver.IssueOutputMetadata{
							{
								Receivers: []*driver.AuditableIdentity{
									{Identity: Identity("recipient2")},
								},
							},
						},
					},
				},
			},
		},
	}

	issues := r.Issues()
	require.Len(t, issues, 2)
	assert.Equal(t, Identity("issuer1"), issues[0].Issuer)
	assert.Len(t, issues[0].Receivers, 1)
	assert.Equal(t, Identity("recipient1"), issues[0].Receivers[0])
	assert.Len(t, issues[0].ExtraSigners, 1)
	assert.Equal(t, Identity("signer1"), issues[0].ExtraSigners[0])

	assert.Equal(t, Identity("issuer2"), issues[1].Issuer)
	assert.Len(t, issues[1].Receivers, 1)
	assert.Equal(t, Identity("recipient2"), issues[1].Receivers[0])
}

// TestRequest_Transfers tests Transfers method with populated metadata
func TestRequest_Transfers(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					TransferMetadata: &driver.TransferMetadata{
						Inputs: []*driver.TransferInputMetadata{
							{
								Senders: []*driver.AuditableIdentity{
									{Identity: Identity("sender1")},
								},
							},
						},
						Outputs: []*driver.TransferOutputMetadata{
							{
								Receivers: []*driver.AuditableIdentity{
									{Identity: Identity("receiver1")},
								},
							},
						},
						ExtraSigners: []driver.AuditableIdentity{
							{Identity: Identity("extra1")},
						},
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer1"),
						},
					},
				},
			},
		},
	}

	transfers := r.Transfers()
	require.Len(t, transfers, 1)
	assert.Len(t, transfers[0].Senders, 1)
	assert.Equal(t, Identity("sender1"), transfers[0].Senders[0])
	assert.Len(t, transfers[0].Receivers, 1)
	assert.Equal(t, Identity("receiver1"), transfers[0].Receivers[0])
	assert.Len(t, transfers[0].ExtraSigners, 1)
	assert.Equal(t, Identity("extra1"), transfers[0].ExtraSigners[0])
	assert.Equal(t, Identity("issuer1"), transfers[0].Issuer)
}

// TestRequest_TransferSigners tests TransferSigners with populated metadata
func TestRequest_TransferSigners(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					TransferMetadata: &driver.TransferMetadata{
						Inputs: []*driver.TransferInputMetadata{
							{
								Senders: []*driver.AuditableIdentity{
									{Identity: Identity("sender1")},
									{Identity: Identity("sender2")},
								},
							},
						},
						Outputs: []*driver.TransferOutputMetadata{
							{
								Receivers: []*driver.AuditableIdentity{
									{Identity: Identity("receiver1")},
								},
							},
						},
						ExtraSigners: []driver.AuditableIdentity{
							{Identity: Identity("extra1")},
						},
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer1"),
						},
					},
				},
			},
		},
	}

	signers := r.TransferSigners()
	require.Len(t, signers, 4) // 2 senders + 1 issuer + 1 extra
	assert.Equal(t, Identity("sender1"), signers[0])
	assert.Equal(t, Identity("sender2"), signers[1])
	assert.Equal(t, Identity("issuer1"), signers[2])
	assert.Equal(t, Identity("extra1"), signers[3])
}

// TestRequest_IssueSigners tests IssueSigners with populated metadata
func TestRequest_IssueSigners(t *testing.T) {
	r := &Request{
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					IssueMetadata: &driver.IssueMetadata{
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer1"),
						},
						ExtraSigners: []driver.AuditableIdentity{
							{Identity: Identity("extra1")},
							{Identity: Identity("extra2")},
						},
					},
				},
				{
					ActionID: 1,
					IssueMetadata: &driver.IssueMetadata{
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer2"),
						},
					},
				},
			},
		},
	}

	signers := r.IssueSigners()
	require.Len(t, signers, 4) // 2 issuers + 2 extras
	assert.Equal(t, Identity("issuer1"), signers[0])
	assert.Equal(t, Identity("extra1"), signers[1])
	assert.Equal(t, Identity("extra2"), signers[2])
	assert.Equal(t, Identity("issuer2"), signers[3])
}

// TestRequest_SetSignatures tests SetSignatures method
func TestRequest_SetSignatures(t *testing.T) {
	r := &Request{
		Actions: &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
			},
		},
		Metadata: &driver.TokenRequestMetadata{
			Actions: []*driver.ActionMetadataEntry{
				{
					ActionID: 0,
					IssueMetadata: &driver.IssueMetadata{
						Issuer: driver.AuditableIdentity{
							Identity: Identity("issuer1"),
						},
					},
				},
				{
					ActionID: 1,
					TransferMetadata: &driver.TransferMetadata{
						Inputs: []*driver.TransferInputMetadata{
							{
								Senders: []*driver.AuditableIdentity{
									{Identity: Identity("sender1")},
								},
							},
						},
						Outputs: []*driver.TransferOutputMetadata{
							{
								Receivers: []*driver.AuditableIdentity{
									{Identity: Identity("receiver1")},
								},
							},
						},
					},
				},
			},
		},
		TokenService: &ManagementService{
			logger: logging.MustGetLogger(),
		},
	}

	// Test with all signatures present
	sigmas := map[string][]byte{
		Identity("issuer1").UniqueID(): []byte("sig1"),
		Identity("sender1").UniqueID(): []byte("sig2"),
	}

	allPresent := r.SetSignatures(sigmas)
	assert.True(t, allPresent)
	require.Len(t, r.Actions.Signatures, 2)
	require.NotNil(t, r.Actions.Signatures[0].Action)
	require.NotNil(t, r.Actions.Signatures[1].Action)
	assert.Equal(t, []byte("sig1"), r.Actions.Signatures[0].Action.Signature)
	assert.Equal(t, uint32(0), r.Actions.Signatures[0].Action.ActionID)
	assert.Equal(t, []byte("sig2"), r.Actions.Signatures[1].Action.Signature)
	assert.Equal(t, uint32(1), r.Actions.Signatures[1].Action.ActionID)

	// Test with missing signature
	r.Actions.Signatures = nil
	sigmas = map[string][]byte{
		Identity("issuer1").UniqueID(): []byte("sig1"),
	}

	allPresent = r.SetSignatures(sigmas)
	assert.False(t, allPresent)
	require.Len(t, r.Actions.Signatures, 2)
	require.NotNil(t, r.Actions.Signatures[0].Action)
	require.NotNil(t, r.Actions.Signatures[1].Action)
	assert.Equal(t, []byte("sig1"), r.Actions.Signatures[0].Action.Signature)
	assert.Nil(t, r.Actions.Signatures[1].Action.Signature)
}

// TestRequest_cleanupInputIDs tests the cleanupInputIDs utility function
func TestRequest_cleanupInputIDs(t *testing.T) {
	r := &Request{}

	// Test with nil values
	input := []*token.ID{
		{TxId: "tx1", Index: 0},
		nil,
		{TxId: "tx2", Index: 1},
		nil,
		{TxId: "tx3", Index: 2},
	}

	result := r.cleanupInputIDs(input)
	require.Len(t, result, 3)
	assert.Equal(t, "tx1", result[0].TxId)
	assert.Equal(t, "tx2", result[1].TxId)
	assert.Equal(t, "tx3", result[2].TxId)

	// Test with no nil values
	input = []*token.ID{
		{TxId: "tx1", Index: 0},
		{TxId: "tx2", Index: 1},
	}

	result = r.cleanupInputIDs(input)
	require.Len(t, result, 2)

	// Test with all nil values
	input = []*token.ID{nil, nil, nil}
	result = r.cleanupInputIDs(input)
	assert.Empty(t, result)

	// Test with empty slice
	input = []*token.ID{}
	result = r.cleanupInputIDs(input)
	assert.Empty(t, result)
}

// TestRequest_GetMetadata tests GetMetadata method
func TestRequest_GetMetadata(t *testing.T) {
	mockTokensService := &driver2.TokensService{}
	mockWalletService := &driver2.WalletService{}
	mockTMS := &driver2.TokenManagerService{}
	mockTMS.TokensServiceReturns(mockTokensService)
	mockTMS.WalletServiceReturns(mockWalletService)

	tms := &ManagementService{
		tms:    mockTMS,
		logger: logging.MustGetLogger(),
	}

	r := &Request{
		TokenService: tms,
		Metadata: &driver.TokenRequestMetadata{
			Application: map[string][]byte{"key": []byte("value")},
		},
	}

	metadata, err := r.GetMetadata()
	require.NoError(t, err)
	assert.NotNil(t, metadata)
	assert.Equal(t, mockTokensService, metadata.TokenService)
	assert.Equal(t, mockWalletService, metadata.WalletService)
	assert.Equal(t, r.Metadata, metadata.TokenRequestMetadata)
	assert.NotNil(t, metadata.Logger)
}

// TestRequest_PublicParamsHash tests PublicParamsHash method
func TestRequest_PublicParamsHash(t *testing.T) {
	expectedHash := PPHash("test-hash-123")
	mockPPM := &driver2.PublicParamsManager{}
	mockPPM.PublicParamsHashReturns(expectedHash)

	tms := &ManagementService{
		publicParametersManager: &PublicParametersManager{
			ppm: mockPPM,
		},
	}

	r := &Request{
		TokenService: tms,
	}

	hash := r.PublicParamsHash()
	assert.Equal(t, expectedHash, hash)
}

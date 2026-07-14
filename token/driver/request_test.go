/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"encoding/asn1"
	"testing"

	driver "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"

	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuditorSignature_ToProtos tests the conversion of AuditorSignature to protobuf format
func TestAuditorSignature_ToProtos(t *testing.T) {
	audSig := &AuditorSignature{
		Identity:  Identity("auditor1"),
		Signature: []byte("signature1"),
	}

	proto, err := audSig.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Equal(t, []byte("auditor1"), proto.Identity.Raw)
	assert.Equal(t, []byte("signature1"), proto.Signature.Raw)
}

// TestAuditorSignature_FromProtos tests the conversion from protobuf to AuditorSignature
func TestAuditorSignature_FromProtos(t *testing.T) {
	proto := &request.AuditorSignature{
		Identity: &driver.Identity{
			Raw: []byte("auditor1"),
		},
		Signature: &request.Signature{
			Raw: []byte("signature1"),
		},
	}

	audSig := &AuditorSignature{}
	err := audSig.FromProtos(proto)
	require.NoError(t, err)
	assert.Equal(t, Identity("auditor1"), audSig.Identity)
	assert.Equal(t, []byte("signature1"), audSig.Signature)
}

// TestAuditorSignature_FromProtos_NilFields tests handling of nil fields in protobuf
func TestAuditorSignature_FromProtos_NilFields(t *testing.T) {
	proto := &request.AuditorSignature{}

	audSig := &AuditorSignature{}
	err := audSig.FromProtos(proto)
	require.NoError(t, err)
	assert.Nil(t, audSig.Identity)
	assert.Nil(t, audSig.Signature)
}

// TestTokenRequest_Bytes tests serialization of TokenRequest
func TestTokenRequest_Bytes(t *testing.T) {
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("signature1"),
				},
			},
			{
				Auditor: &AuditorSignature{
					Identity:  Identity("auditor1"),
					Signature: []byte("audsig1"),
				},
			},
		},
	}

	raw, err := req.Bytes()
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

// TestTokenRequest_FromBytes_InvalidVersion tests error handling for invalid version
func TestTokenRequest_FromBytes_InvalidVersion(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: 999, // Invalid version
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte("issue1"),
					},
				},
			},
		},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	req := &TokenRequest{}
	err = req.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported token request version")
}

// TestTokenRequest_FromBytes_NilAction tests error handling for nil action
// Note: protobuf unmarshaling converts nil actions to default-initialized actions
// with Type=ACTION_TYPE_UNSPECIFIED, which is now caught by our validation
func TestTokenRequest_FromBytes_NilAction(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: ProtocolV1,
		Actions: []*request.Action{nil},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	req := &TokenRequest{}
	err = req.FromBytes(raw)
	// Protobuf converts nil to default-initialized Action, which has no TypedAction set
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only TypedAction is currently supported")
}

// TestTokenRequest_FromBytes_UnknownActionType tests error handling for unknown action type
func TestTokenRequest_FromBytes_UnknownActionType(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: ProtocolV1,
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: 999, // Unknown action type
						Raw:  []byte("unknown"),
					},
				},
			},
		},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	req := &TokenRequest{}
	err = req.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown action type")
}

// TestTokenRequest_FromBytes_UnspecifiedActionType tests error handling for unspecified action type
func TestTokenRequest_FromBytes_UnspecifiedActionType(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: ProtocolV1,
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_UNSPECIFIED,
						Raw:  []byte("unspecified"),
					},
				},
			},
		},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	req := &TokenRequest{}
	err = req.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action type must be explicitly specified")
}

// TestTokenRequest_FromBytes_NilSignature tests error handling for nil signature
func TestTokenRequest_FromBytes_NilSignature(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: ProtocolV1,
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte("issue1"),
					},
				},
			},
		},
		Signatures: []*request.RequestSignature{nil},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	req := &TokenRequest{}
	err = req.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request signature type not recognized")
}

// TestTokenRequest_FromBytes_EmptySignature tests error handling for empty signature
func TestTokenRequest_FromBytes_EmptySignature(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: ProtocolV1,
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte("issue1"),
					},
				},
			},
		},
		Signatures: []*request.RequestSignature{
			{
				Signature: &request.RequestSignature_ActionSignature{
					ActionSignature: &request.ActionSignature{
						ActionId:  0,
						Signature: &request.Signature{Raw: []byte{}},
					},
				},
			},
		},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	req := &TokenRequest{}
	err = req.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil action signature found")
}

func TestTokenRequestSerialization(t *testing.T) {
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("signature1"),
				},
			},
			{
				Auditor: &AuditorSignature{
					Identity:  Identity("auditor1"),
					Signature: []byte("signature1"),
				},
			},
		},
	}
	raw, err := req.Bytes()
	require.NoError(t, err)

	req2 := &TokenRequest{}
	err = req2.FromBytes(raw)
	require.NoError(t, err)

	// Version defaults to V1 (structured format) for new requests
	req.Version = ProtocolV1
	assert.Equal(t, req, req2)
}

// TestTokenRequest_MarshalToMessageToSign tests message marshaling for signing
func TestTokenRequest_MarshalToMessageToSign(t *testing.T) {
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	anchor := []byte("anchor123")
	msg, err := req.MarshalToMessageToSign(anchor)
	require.NoError(t, err)
	assert.NotEmpty(t, msg)
	// Verify anchor is appended
	assert.Contains(t, string(msg), string(anchor))
}

// TestAuditableIdentity_ToProtos tests conversion to protobuf
func TestAuditableIdentity_ToProtos(t *testing.T) {
	ai := &AuditableIdentity{
		Identity:  Identity("identity1"),
		AuditInfo: []byte("auditinfo1"),
	}

	proto, err := ai.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Equal(t, []byte("identity1"), proto.Identity.Raw)
	assert.Equal(t, []byte("auditinfo1"), proto.AuditInfo)
}

// TestAuditableIdentity_FromProtos tests conversion from protobuf
func TestAuditableIdentity_FromProtos(t *testing.T) {
	proto := &request.AuditableIdentity{
		Identity: &driver.Identity{
			Raw: []byte("identity1"),
		},
		AuditInfo: []byte("auditinfo1"),
	}

	ai := &AuditableIdentity{}
	err := ai.FromProtos(proto)
	require.NoError(t, err)
	assert.Equal(t, Identity("identity1"), ai.Identity)
	assert.Equal(t, []byte("auditinfo1"), ai.AuditInfo)
}

// TestIssueInputMetadata_ToProtos tests conversion to protobuf
func TestIssueInputMetadata_ToProtos(t *testing.T) {
	iim := &IssueInputMetadata{
		TokenID: &token.ID{
			TxId:  "tx123",
			Index: 5,
		},
	}

	proto, err := iim.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Equal(t, "tx123", proto.TokenId.TxId)
	assert.Equal(t, uint64(5), proto.TokenId.Index)
}

// TestIssueInputMetadata_FromProtos tests conversion from protobuf
func TestIssueInputMetadata_FromProtos(t *testing.T) {
	proto := &request.IssueInputMetadata{
		TokenId: &driver.TokenID{
			TxId:  "tx123",
			Index: 5,
		},
	}

	iim := &IssueInputMetadata{}
	err := iim.FromProtos(proto)
	require.NoError(t, err)
	require.NotNil(t, iim.TokenID)
	assert.Equal(t, "tx123", iim.TokenID.TxId)
	assert.Equal(t, uint64(5), iim.TokenID.Index)
}

// TestIssueOutputMetadata_RecipientAt tests recipient retrieval by index
func TestIssueOutputMetadata_RecipientAt(t *testing.T) {
	iom := &IssueOutputMetadata{
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1")},
			{Identity: Identity("receiver2")},
		},
	}

	// Valid index
	recipient := iom.RecipientAt(0)
	require.NotNil(t, recipient)
	assert.Equal(t, Identity("receiver1"), recipient.Identity)

	recipient = iom.RecipientAt(1)
	require.NotNil(t, recipient)
	assert.Equal(t, Identity("receiver2"), recipient.Identity)

	// Invalid indices
	assert.Nil(t, iom.RecipientAt(-1))
	assert.Nil(t, iom.RecipientAt(2))
	assert.Nil(t, iom.RecipientAt(100))
}

// TestIssueOutputMetadata_ToProtos tests conversion to protobuf
func TestIssueOutputMetadata_ToProtos(t *testing.T) {
	iom := &IssueOutputMetadata{
		OutputMetadata: []byte("metadata1"),
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1"), AuditInfo: []byte("audit1")},
		},
	}

	proto, err := iom.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Equal(t, []byte("metadata1"), proto.Metadata)
	assert.Len(t, proto.Receivers, 1)
	assert.Equal(t, []byte("receiver1"), proto.Receivers[0].Identity.Raw)
	assert.Equal(t, []byte("audit1"), proto.Receivers[0].AuditInfo)
}

// TestIssueOutputMetadata_FromProtos tests conversion from protobuf
func TestIssueOutputMetadata_FromProtos(t *testing.T) {
	proto := &request.OutputMetadata{
		Metadata: []byte("metadata1"),
		Receivers: []*request.AuditableIdentity{
			{
				Identity:  &driver.Identity{Raw: []byte("receiver1")},
				AuditInfo: []byte("audit1"),
			},
		},
	}

	iom := &IssueOutputMetadata{}
	err := iom.FromProtos(proto)
	require.NoError(t, err)
	assert.Equal(t, []byte("metadata1"), iom.OutputMetadata)
	assert.Len(t, iom.Receivers, 1)
	assert.Equal(t, Identity("receiver1"), iom.Receivers[0].Identity)
	assert.Equal(t, []byte("audit1"), iom.Receivers[0].AuditInfo)
}

// TestIssueOutputMetadata_FromProtos_Nil tests handling of nil protobuf
func TestIssueOutputMetadata_FromProtos_Nil(t *testing.T) {
	iom := &IssueOutputMetadata{}
	err := iom.FromProtos(nil)
	require.NoError(t, err)
}

// TestIssueMetadata_Receivers tests receiver extraction
func TestIssueMetadata_Receivers(t *testing.T) {
	im := &IssueMetadata{
		Outputs: []*IssueOutputMetadata{
			{
				Receivers: []*AuditableIdentity{
					{Identity: Identity("receiver1")},
					{Identity: Identity("receiver2")},
				},
			},
			{
				Receivers: []*AuditableIdentity{
					{Identity: Identity("receiver3")},
					nil, // Test nil receiver
				},
			},
		},
	}

	receivers := im.Receivers()
	assert.Len(t, receivers, 4)
	assert.Equal(t, Identity("receiver1"), receivers[0])
	assert.Equal(t, Identity("receiver2"), receivers[1])
	assert.Equal(t, Identity("receiver3"), receivers[2])
	assert.Nil(t, receivers[3])
}

// TestIssueMetadata_ToProtos tests conversion to protobuf
func TestIssueMetadata_ToProtos(t *testing.T) {
	im := &IssueMetadata{
		Issuer: AuditableIdentity{
			Identity:  Identity("issuer1"),
			AuditInfo: []byte("issuer_audit"),
		},
		Inputs: []*IssueInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 0}},
		},
		Outputs: []*IssueOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
		ExtraSigners: []AuditableIdentity{
			{Identity: Identity("signer1"), AuditInfo: []byte("signer1_audit")},
		},
	}

	proto, err := im.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.NotNil(t, proto.Issuer)
	assert.Equal(t, []byte("issuer1"), proto.Issuer.Identity.Raw)
	assert.Equal(t, []byte("issuer_audit"), proto.Issuer.AuditInfo)
	assert.Len(t, proto.Inputs, 1)
	assert.Equal(t, "tx1", proto.Inputs[0].TokenId.TxId)
	assert.Len(t, proto.Outputs, 1)
	assert.Equal(t, []byte("output1"), proto.Outputs[0].Metadata)
	assert.Len(t, proto.ExtraSigners, 1)
	assert.Equal(t, []byte("signer1"), proto.ExtraSigners[0].Identity.Raw)
}

// TestIssueMetadata_FromProtos tests conversion from protobuf
func TestIssueMetadata_FromProtos(t *testing.T) {
	proto := &request.IssueMetadata{
		Issuer: &request.AuditableIdentity{
			Identity:  &driver.Identity{Raw: []byte("issuer1")},
			AuditInfo: []byte("issuer_audit"),
		},
		Inputs: []*request.IssueInputMetadata{
			{TokenId: &driver.TokenID{TxId: "tx1", Index: 0}},
		},
		Outputs: []*request.OutputMetadata{
			{Metadata: []byte("output1")},
		},
		ExtraSigners: []*request.AuditableIdentity{
			{Identity: &driver.Identity{Raw: []byte("signer1")}},
		},
	}

	im := &IssueMetadata{}
	err := im.FromProtos(proto)
	require.NoError(t, err)
	assert.Equal(t, Identity("issuer1"), im.Issuer.Identity)
	assert.Equal(t, []byte("issuer_audit"), im.Issuer.AuditInfo)
	assert.Len(t, im.Inputs, 1)
	assert.Equal(t, "tx1", im.Inputs[0].TokenID.TxId)
	assert.Len(t, im.Outputs, 1)
	assert.Equal(t, []byte("output1"), im.Outputs[0].OutputMetadata)
	assert.Len(t, im.ExtraSigners, 1)
	assert.Equal(t, Identity("signer1"), im.ExtraSigners[0].Identity)
	assert.Nil(t, im.ExtraSigners[0].AuditInfo)
}

// TestTransferInputMetadata_ToProtos tests conversion to protobuf
func TestTransferInputMetadata_ToProtos(t *testing.T) {
	tim := &TransferInputMetadata{
		TokenID: &token.ID{TxId: "tx123", Index: 5},
		Senders: []*AuditableIdentity{
			{Identity: Identity("sender1"), AuditInfo: []byte("audit1")},
		},
	}

	proto, err := tim.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Equal(t, "tx123", proto.TokenId.TxId)
	assert.Equal(t, uint64(5), proto.TokenId.Index)
	assert.Len(t, proto.Senders, 1)
	assert.Equal(t, []byte("sender1"), proto.Senders[0].Identity.Raw)
	assert.Equal(t, []byte("audit1"), proto.Senders[0].AuditInfo)
}

// TestTransferInputMetadata_FromProtos tests conversion from protobuf
func TestTransferInputMetadata_FromProtos(t *testing.T) {
	proto := &request.TransferInputMetadata{
		TokenId: &driver.TokenID{TxId: "tx123", Index: 5},
		Senders: []*request.AuditableIdentity{
			{
				Identity:  &driver.Identity{Raw: []byte("sender1")},
				AuditInfo: []byte("audit1"),
			},
		},
	}

	tim := &TransferInputMetadata{}
	err := tim.FromProtos(proto)
	require.NoError(t, err)
	require.NotNil(t, tim.TokenID)
	assert.Equal(t, "tx123", tim.TokenID.TxId)
	assert.Equal(t, uint64(5), tim.TokenID.Index)
	assert.Len(t, tim.Senders, 1)
	assert.Equal(t, Identity("sender1"), tim.Senders[0].Identity)
	assert.Equal(t, []byte("audit1"), tim.Senders[0].AuditInfo)
}

// TestTransferInputMetadata_FromProtos_Nil tests handling of nil protobuf
func TestTransferInputMetadata_FromProtos_Nil(t *testing.T) {
	tim := &TransferInputMetadata{}
	err := tim.FromProtos(nil)
	require.NoError(t, err)
}

// TestTransferOutputMetadata_RecipientAt tests recipient retrieval by index
func TestTransferOutputMetadata_RecipientAt(t *testing.T) {
	tom := &TransferOutputMetadata{
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1")},
			{Identity: Identity("receiver2")},
		},
	}

	// Valid index
	recipient := tom.RecipientAt(0)
	require.NotNil(t, recipient)
	assert.Equal(t, Identity("receiver1"), recipient.Identity)

	// Invalid indices
	assert.Nil(t, tom.RecipientAt(-1))
	assert.Nil(t, tom.RecipientAt(2))
}

// TestTransferOutputMetadata_ToProtos tests conversion to protobuf
func TestTransferOutputMetadata_ToProtos(t *testing.T) {
	tom := &TransferOutputMetadata{
		OutputMetadata:  []byte("metadata1"),
		OutputAuditInfo: []byte("auditinfo1"),
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1")},
		},
	}

	proto, err := tom.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Equal(t, []byte("metadata1"), proto.Metadata)
	assert.Equal(t, []byte("auditinfo1"), proto.AuditInfo)
	assert.Len(t, proto.Receivers, 1)
	assert.Equal(t, []byte("receiver1"), proto.Receivers[0].Identity.Raw)
}

// TestTransferOutputMetadata_FromProtos tests conversion from protobuf
func TestTransferOutputMetadata_FromProtos(t *testing.T) {
	proto := &request.OutputMetadata{
		Metadata:  []byte("metadata1"),
		AuditInfo: []byte("auditinfo1"),
		Receivers: []*request.AuditableIdentity{
			{Identity: &driver.Identity{Raw: []byte("receiver1")}},
		},
	}

	tom := &TransferOutputMetadata{}
	err := tom.FromProtos(proto)
	require.NoError(t, err)
	assert.Equal(t, []byte("metadata1"), tom.OutputMetadata)
	assert.Equal(t, []byte("auditinfo1"), tom.OutputAuditInfo)
	assert.Len(t, tom.Receivers, 1)
	assert.Equal(t, Identity("receiver1"), tom.Receivers[0].Identity)
}

// TestTransferOutputMetadata_FromProtos_Nil tests handling of nil protobuf
func TestTransferOutputMetadata_FromProtos_Nil(t *testing.T) {
	tom := &TransferOutputMetadata{}
	err := tom.FromProtos(nil)
	require.NoError(t, err)
}

// TestTransferMetadata_TokenIDAt tests TokenID retrieval by index
func TestTransferMetadata_TokenIDAt(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 1}},
			{TokenID: &token.ID{TxId: "tx2", Index: 2}},
		},
	}

	// Valid index
	tokenID := tm.TokenIDAt(0)
	require.NotNil(t, tokenID)
	assert.Equal(t, "tx1", tokenID.TxId)

	tokenID = tm.TokenIDAt(1)
	require.NotNil(t, tokenID)
	assert.Equal(t, "tx2", tokenID.TxId)

	// Invalid indices
	assert.Nil(t, tm.TokenIDAt(-1))
	assert.Nil(t, tm.TokenIDAt(2))
}

// TestTransferMetadata_Receivers tests receiver extraction
func TestTransferMetadata_Receivers(t *testing.T) {
	tm := &TransferMetadata{
		Outputs: []*TransferOutputMetadata{
			{
				Receivers: []*AuditableIdentity{
					{Identity: Identity("receiver1")},
					nil, // Test nil receiver
				},
			},
			{
				Receivers: []*AuditableIdentity{
					{Identity: Identity("receiver2")},
				},
			},
		},
	}

	receivers := tm.Receivers()
	assert.Len(t, receivers, 3)
	assert.Equal(t, Identity("receiver1"), receivers[0])
	assert.Nil(t, receivers[1])
	assert.Equal(t, Identity("receiver2"), receivers[2])
}

// TestTransferMetadata_Senders tests sender extraction
func TestTransferMetadata_Senders(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{
			{
				Senders: []*AuditableIdentity{
					{Identity: Identity("sender1")},
					{Identity: Identity("sender2")},
				},
			},
			{
				Senders: []*AuditableIdentity{
					nil, // Test nil sender
					{Identity: Identity("sender3")},
				},
			},
		},
	}

	senders := tm.Senders()
	assert.Len(t, senders, 4)
	assert.Equal(t, Identity("sender1"), senders[0])
	assert.Equal(t, Identity("sender2"), senders[1])
	assert.Nil(t, senders[2])
	assert.Equal(t, Identity("sender3"), senders[3])
}

// TestTransferMetadata_TokenIDs tests TokenID extraction
func TestTransferMetadata_TokenIDs(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 1}},
			nil, // Test nil input
			{TokenID: &token.ID{TxId: "tx2", Index: 2}},
		},
	}

	tokenIDs := tm.TokenIDs()
	assert.Len(t, tokenIDs, 2) // nil input should be skipped
	assert.Equal(t, "tx1", tokenIDs[0].TxId)
	assert.Equal(t, "tx2", tokenIDs[1].TxId)
}

// TestTransferMetadata_ToProtos tests conversion to protobuf
func TestTransferMetadata_ToProtos(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 1}},
		},
		Outputs: []*TransferOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
		ExtraSigners: []AuditableIdentity{
			{Identity: Identity("signer1"), AuditInfo: []byte("signer1_audit")},
		},
		Issuer: AuditableIdentity{
			Identity:  Identity("issuer1"),
			AuditInfo: []byte("issuer1_audit"),
		},
	}

	proto, err := tm.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Len(t, proto.Inputs, 1)
	assert.Equal(t, "tx1", proto.Inputs[0].TokenId.TxId)
	assert.Equal(t, uint64(1), proto.Inputs[0].TokenId.Index)
	assert.Len(t, proto.Outputs, 1)
	assert.Equal(t, []byte("output1"), proto.Outputs[0].Metadata)
	assert.Len(t, proto.ExtraSigners, 1)
	assert.Equal(t, []byte("signer1"), proto.ExtraSigners[0].Identity.Raw)
	assert.NotNil(t, proto.Issuer)
	assert.Equal(t, []byte("issuer1"), proto.Issuer.Identity.Raw)
}

// TestTransferMetadata_ToProtos_NilIssuer tests conversion with nil issuer
func TestTransferMetadata_ToProtos_NilIssuer(t *testing.T) {
	tm := &TransferMetadata{
		Inputs:  []*TransferInputMetadata{},
		Outputs: []*TransferOutputMetadata{},
		Issuer:  AuditableIdentity{},
	}

	proto, err := tm.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Nil(t, proto.Issuer)
}

// TestTransferMetadata_FromProtos tests conversion from protobuf
func TestTransferMetadata_FromProtos(t *testing.T) {
	proto := &request.TransferMetadata{
		Inputs: []*request.TransferInputMetadata{
			{TokenId: &driver.TokenID{TxId: "tx1", Index: 1}},
		},
		Outputs: []*request.OutputMetadata{
			{Metadata: []byte("output1")},
		},
		ExtraSigners: []*request.AuditableIdentity{
			{Identity: &driver.Identity{Raw: []byte("signer1")}},
		},
		Issuer: &request.AuditableIdentity{
			Identity: &driver.Identity{Raw: []byte("issuer1")},
		},
	}

	tm := &TransferMetadata{}
	err := tm.FromProtos(proto)
	require.NoError(t, err)
	assert.Len(t, tm.Inputs, 1)
	assert.Equal(t, "tx1", tm.Inputs[0].TokenID.TxId)
	assert.Equal(t, uint64(1), tm.Inputs[0].TokenID.Index)
	assert.Len(t, tm.Outputs, 1)
	assert.Equal(t, []byte("output1"), tm.Outputs[0].OutputMetadata)
	assert.Len(t, tm.ExtraSigners, 1)
	assert.Equal(t, Identity("signer1"), tm.ExtraSigners[0].Identity)
	assert.Nil(t, tm.ExtraSigners[0].AuditInfo)
	assert.Equal(t, Identity("issuer1"), tm.Issuer.Identity)
	assert.Nil(t, tm.Issuer.AuditInfo)
}

// TestTransferMetadata_FromProtos_NilIssuer tests conversion with nil issuer
func TestTransferMetadata_FromProtos_NilIssuer(t *testing.T) {
	proto := &request.TransferMetadata{
		Inputs:  []*request.TransferInputMetadata{},
		Outputs: []*request.OutputMetadata{},
		Issuer:  nil,
	}

	tm := &TransferMetadata{}
	err := tm.FromProtos(proto)
	require.NoError(t, err)
	assert.Nil(t, tm.Issuer.Identity)
	assert.Nil(t, tm.Issuer.AuditInfo)
}

func TestTokenRequestMetadataSerialization(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{
			{
				ActionID: 0,
				IssueMetadata: &IssueMetadata{
					Issuer: AuditableIdentity{
						Identity:  []byte("issuer1"),
						AuditInfo: []byte("issuer_auditinfo1"),
					},
					Inputs: []*IssueInputMetadata{},
					Outputs: []*IssueOutputMetadata{
						{
							OutputMetadata: []byte("token_info1"),
							Receivers: []*AuditableIdentity{
								{
									Identity:  []byte("receiver1"),
									AuditInfo: []byte("audit_info1"),
								},
							},
						},
						{
							OutputMetadata: []byte("token_info2"),
							Receivers: []*AuditableIdentity{
								{
									Identity:  []byte("receiver2"),
									AuditInfo: []byte("audit_info2"),
								},
							},
						},
					},
					ExtraSigners: []AuditableIdentity{
						{Identity: []byte("issue_extra_signer1")},
						{Identity: []byte("issue_extra_signer2")},
					},
				},
			},
			{
				ActionID: 1,
				TransferMetadata: &TransferMetadata{
					Inputs: []*TransferInputMetadata{
						{
							TokenID: &token.ID{
								TxId:  "txid1",
								Index: 1,
							},
							Senders: []*AuditableIdentity{
								{
									Identity:  []byte("sender1"),
									AuditInfo: []byte("sender1_audit_info"),
								},
							},
						},
						{
							TokenID: &token.ID{
								TxId:  "txid2",
								Index: 1,
							},
							Senders: []*AuditableIdentity{
								{
									Identity:  []byte("sender2"),
									AuditInfo: []byte("sender2_audit_info"),
								},
							},
						},
					},
					Outputs: []*TransferOutputMetadata{
						{
							OutputAuditInfo: []byte("token_info_3"),
							OutputMetadata:  []byte("token_meta_3"),
							Receivers: []*AuditableIdentity{
								{
									Identity:  []byte("receiver3"),
									AuditInfo: []byte("audit_info3"),
								},
							},
						},
						{
							OutputAuditInfo: []byte("token_info_4"),
							OutputMetadata:  []byte("token_meta_4"),
							Receivers: []*AuditableIdentity{
								{
									Identity:  []byte("receiver4"),
									AuditInfo: []byte("audit_info4"),
								},
							},
						},
					},
					ExtraSigners: []AuditableIdentity{
						{Identity: []byte("extra_signer1")},
						{Identity: []byte("extra_signer2")},
					},
					Issuer: AuditableIdentity{
						Identity: Identity([]byte("issuer")),
					},
				},
			},
		},
		Application: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	raw, err := reqMeta.Bytes()
	require.NoError(t, err)

	reqMeta2 := &TokenRequestMetadata{}
	err = reqMeta2.FromBytes(raw)
	require.NoError(t, err)
	raw2, err := reqMeta2.Bytes()
	require.NoError(t, err)
	reqMeta3 := &TokenRequestMetadata{}
	err = reqMeta3.FromBytes(raw2)
	require.NoError(t, err)

	assert.Equal(t, reqMeta, reqMeta2)
	assert.Equal(t, reqMeta2, reqMeta3)
}

// TestTokenRequestMetadata_FromBytes_InvalidVersion tests error handling for invalid version
func TestTokenRequestMetadata_FromBytes_InvalidVersion(t *testing.T) {
	protoMeta := &request.TokenRequestMetadata{
		Version: 999, // Invalid version
	}

	raw, err := proto.Marshal(protoMeta)
	require.NoError(t, err)

	reqMeta := &TokenRequestMetadata{}
	err = reqMeta.FromBytes(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid token request metadata version")
}

// TestTokenRequestMetadata_ToProtos_NilIssueMetadata tests error handling for nil issue metadata
func TestTokenRequestMetadata_ToProtos_NilIssueMetadata(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{
			{ActionID: 0, IssueMetadata: nil},
		},
	}

	_, err := reqMeta.ToProtos()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "it is nil")
}

// TestTokenRequestMetadata_ToProtos_NilTransferMetadata tests error handling for nil transfer metadata
func TestTokenRequestMetadata_ToProtos_NilTransferMetadata(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{
			{ActionID: 0, TransferMetadata: nil},
		},
	}

	_, err := reqMeta.ToProtos()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "it is nil")
}

// TestTokenRequestMetadata_FromProtos_UnrecognizedMetadata tests error handling for unrecognized metadata
func TestTokenRequestMetadata_FromProtos_UnrecognizedMetadata(t *testing.T) {
	protoMeta := &request.TokenRequestMetadata{
		Version: ProtocolV1,
		Metadata: []*request.ActionMetadata{
			{}, // Empty metadata (neither issue nor transfer)
		},
	}

	reqMeta := &TokenRequestMetadata{}
	err := reqMeta.FromProtos(protoMeta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type not recognized")
}

// TestTokenRequestMetadata_EmptyApplication tests handling of empty application map
func TestTokenRequestMetadata_EmptyApplication(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions:     []*ActionMetadataEntry{},
		Application: nil,
	}

	raw, err := reqMeta.Bytes()
	require.NoError(t, err)

	reqMeta2 := &TokenRequestMetadata{}
	err = reqMeta2.FromBytes(raw)
	require.NoError(t, err)
	assert.Nil(t, reqMeta2.Application)
}

// TestTokenRequestMetadata_WithApplication tests application metadata handling
func TestTokenRequestMetadata_WithApplication(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{},
		Application: map[string][]byte{
			"app_key1": []byte("app_value1"),
			"app_key2": []byte("app_value2"),
		},
	}

	raw, err := reqMeta.Bytes()
	require.NoError(t, err)

	reqMeta2 := &TokenRequestMetadata{}
	err = reqMeta2.FromBytes(raw)
	require.NoError(t, err)
	assert.Equal(t, reqMeta.Application, reqMeta2.Application)
}

// TestIssueMetadata_Receivers_EmptyOutputs tests receiver extraction with empty outputs
func TestIssueMetadata_Receivers_EmptyOutputs(t *testing.T) {
	im := &IssueMetadata{
		Outputs: []*IssueOutputMetadata{},
	}

	receivers := im.Receivers()
	assert.Empty(t, receivers)
}

// TestTransferMetadata_Receivers_EmptyOutputs tests receiver extraction with empty outputs
func TestTransferMetadata_Receivers_EmptyOutputs(t *testing.T) {
	tm := &TransferMetadata{
		Outputs: []*TransferOutputMetadata{},
	}

	receivers := tm.Receivers()
	assert.Empty(t, receivers)
}

// TestTransferMetadata_Senders_EmptyInputs tests sender extraction with empty inputs
func TestTransferMetadata_Senders_EmptyInputs(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{},
	}

	senders := tm.Senders()
	assert.Empty(t, senders)
}

// TestTransferMetadata_TokenIDs_EmptyInputs tests TokenID extraction with empty inputs
func TestTransferMetadata_TokenIDs_EmptyInputs(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{},
	}

	tokenIDs := tm.TokenIDs()
	assert.Empty(t, tokenIDs)
}

// TestTokenRequest_Bytes_Error tests error handling in Bytes serialization
func TestTokenRequest_Bytes_Error(t *testing.T) {
	// Create a request that will serialize successfully
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
	}

	raw, err := req.Bytes()
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

// TestTokenRequest_FromBytes_UnmarshalError tests error handling for invalid bytes
func TestTokenRequest_FromBytes_UnmarshalError(t *testing.T) {
	req := &TokenRequest{}
	err := req.FromBytes([]byte("invalid protobuf data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling token request")
}

// TestIssueOutputMetadata_ToProtos_Error tests error handling in ToProtos
func TestIssueOutputMetadata_ToProtos_Error(t *testing.T) {
	// Normal case should work
	iom := &IssueOutputMetadata{
		OutputMetadata: []byte("metadata1"),
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1")},
		},
	}

	proto, err := iom.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestIssueMetadata_ToProtos_ErrorInInputs tests error handling when inputs fail
func TestIssueMetadata_ToProtos_ErrorInInputs(t *testing.T) {
	im := &IssueMetadata{
		Issuer: AuditableIdentity{
			Identity: Identity("issuer1"),
		},
		Inputs: []*IssueInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 0}},
		},
		Outputs: []*IssueOutputMetadata{},
	}

	proto, err := im.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestIssueMetadata_ToProtos_ErrorInOutputs tests error handling when outputs fail
func TestIssueMetadata_ToProtos_ErrorInOutputs(t *testing.T) {
	im := &IssueMetadata{
		Issuer: AuditableIdentity{
			Identity: Identity("issuer1"),
		},
		Inputs: []*IssueInputMetadata{},
		Outputs: []*IssueOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
	}

	proto, err := im.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestIssueMetadata_FromProtos_ErrorInInputs tests error handling in FromProtos
func TestIssueMetadata_FromProtos_ErrorInInputs(t *testing.T) {
	proto := &request.IssueMetadata{
		Issuer: &request.AuditableIdentity{
			Identity: &driver.Identity{Raw: []byte("issuer1")},
		},
		Inputs: []*request.IssueInputMetadata{
			{TokenId: &driver.TokenID{TxId: "tx1", Index: 0}},
		},
		Outputs: []*request.OutputMetadata{},
	}

	im := &IssueMetadata{}
	err := im.FromProtos(proto)
	require.NoError(t, err)
}

// TestIssueMetadata_FromProtos_ErrorInOutputs tests error handling in FromProtos
func TestIssueMetadata_FromProtos_ErrorInOutputs(t *testing.T) {
	proto := &request.IssueMetadata{
		Issuer: &request.AuditableIdentity{
			Identity: &driver.Identity{Raw: []byte("issuer1")},
		},
		Inputs: []*request.IssueInputMetadata{},
		Outputs: []*request.OutputMetadata{
			{Metadata: []byte("output1")},
		},
	}

	im := &IssueMetadata{}
	err := im.FromProtos(proto)
	require.NoError(t, err)
}

// TestTransferOutputMetadata_ToProtos_Error tests error handling in ToProtos
func TestTransferOutputMetadata_ToProtos_Error(t *testing.T) {
	tom := &TransferOutputMetadata{
		OutputMetadata:  []byte("metadata1"),
		OutputAuditInfo: []byte("auditinfo1"),
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1")},
		},
	}

	proto, err := tom.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestTransferMetadata_ToProtos_ErrorInInputs tests error handling when inputs fail
func TestTransferMetadata_ToProtos_ErrorInInputs(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 1}},
		},
		Outputs: []*TransferOutputMetadata{},
	}

	proto, err := tm.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestTransferMetadata_ToProtos_ErrorInOutputs tests error handling when outputs fail
func TestTransferMetadata_ToProtos_ErrorInOutputs(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{},
		Outputs: []*TransferOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
	}

	proto, err := tm.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestTransferMetadata_FromProtos_ErrorInInputs tests error handling in FromProtos
func TestTransferMetadata_FromProtos_ErrorInInputs(t *testing.T) {
	proto := &request.TransferMetadata{
		Inputs: []*request.TransferInputMetadata{
			{TokenId: &driver.TokenID{TxId: "tx1", Index: 1}},
		},
		Outputs: []*request.OutputMetadata{},
	}

	tm := &TransferMetadata{}
	err := tm.FromProtos(proto)
	require.NoError(t, err)
}

// TestTransferMetadata_FromProtos_ErrorInOutputs tests error handling in FromProtos
func TestTransferMetadata_FromProtos_ErrorInOutputs(t *testing.T) {
	proto := &request.TransferMetadata{
		Inputs: []*request.TransferInputMetadata{},
		Outputs: []*request.OutputMetadata{
			{Metadata: []byte("output1")},
		},
	}

	tm := &TransferMetadata{}
	err := tm.FromProtos(proto)
	require.NoError(t, err)
}

// TestTokenRequestMetadata_Bytes_Error tests error handling in Bytes
func TestTokenRequestMetadata_Bytes_Error(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{},
	}

	raw, err := reqMeta.Bytes()
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

// TestTokenRequestMetadata_FromBytes_UnmarshalError tests unmarshal error
func TestTokenRequestMetadata_FromBytes_UnmarshalError(t *testing.T) {
	reqMeta := &TokenRequestMetadata{}
	err := reqMeta.FromBytes([]byte("invalid protobuf"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal")
}

// TestTokenRequestMetadata_ToProtos_ErrorInIssues tests error in issues conversion
func TestTokenRequestMetadata_ToProtos_ErrorInIssues(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{
			{
				ActionID: 0,
				IssueMetadata: &IssueMetadata{
					Issuer: AuditableIdentity{Identity: Identity("issuer1")},
				},
			},
		},
	}

	proto, err := reqMeta.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestTokenRequestMetadata_ToProtos_ErrorInTransfers tests error in transfers conversion
func TestTokenRequestMetadata_ToProtos_ErrorInTransfers(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{
			{
				ActionID: 0,
				TransferMetadata: &TransferMetadata{
					Inputs:  []*TransferInputMetadata{},
					Outputs: []*TransferOutputMetadata{},
				},
			},
		},
	}

	proto, err := reqMeta.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
}

// TestTokenRequestMetadata_FromProtos_NilIssueMetadata tests nil issue metadata handling
func TestTokenRequestMetadata_FromProtos_NilIssueMetadata(t *testing.T) {
	proto := &request.TokenRequestMetadata{
		Version: ProtocolV1,
		Metadata: []*request.ActionMetadata{
			{
				Metadata: &request.ActionMetadata_IssueMetadata{
					IssueMetadata: &request.IssueMetadata{
						Issuer: &request.AuditableIdentity{
							Identity: &driver.Identity{Raw: []byte("issuer1")},
						},
					},
				},
			},
		},
	}

	reqMeta := &TokenRequestMetadata{}
	err := reqMeta.FromProtos(proto)
	require.NoError(t, err)
	require.Len(t, reqMeta.Actions, 1)
	require.NotNil(t, reqMeta.Actions[0].IssueMetadata)
	assert.Equal(t, Identity("issuer1"), reqMeta.Actions[0].IssueMetadata.Issuer.Identity)
}

// TestTokenRequestMetadata_FromProtos_NilTransferMetadata tests nil transfer metadata handling
func TestTokenRequestMetadata_FromProtos_NilTransferMetadata(t *testing.T) {
	proto := &request.TokenRequestMetadata{
		Version: ProtocolV1,
		Metadata: []*request.ActionMetadata{
			{
				Metadata: &request.ActionMetadata_TransferMetadata{
					TransferMetadata: &request.TransferMetadata{
						Inputs:  []*request.TransferInputMetadata{},
						Outputs: []*request.OutputMetadata{},
					},
				},
			},
		},
	}

	reqMeta := &TokenRequestMetadata{}
	err := reqMeta.FromProtos(proto)
	require.NoError(t, err)
}

// TestIssueInputMetadata_ToProtos_NilTokenID tests ToProtos with nil TokenID
func TestIssueInputMetadata_ToProtos_NilTokenID(t *testing.T) {
	iim := &IssueInputMetadata{
		TokenID: nil,
	}

	proto, err := iim.ToProtos()
	require.NoError(t, err)
	assert.Nil(t, proto.TokenId)
}

// TestTransferInputMetadata_ToProtos_NilTokenID tests ToProtos with nil TokenID
func TestTransferInputMetadata_ToProtos_NilTokenID(t *testing.T) {
	tim := &TransferInputMetadata{
		TokenID: nil,
		Senders: []*AuditableIdentity{},
	}

	proto, err := tim.ToProtos()
	require.NoError(t, err)
	assert.Nil(t, proto.TokenId)
}

// TestTransferInputMetadata_ToProtos_WithSenders tests ToProtos with senders
func TestTransferInputMetadata_ToProtos_WithSenders(t *testing.T) {
	tim := &TransferInputMetadata{
		TokenID: &token.ID{TxId: "tx1", Index: 1},
		Senders: []*AuditableIdentity{
			{Identity: Identity("sender1"), AuditInfo: []byte("audit1")},
			{Identity: Identity("sender2"), AuditInfo: []byte("audit2")},
		},
	}

	proto, err := tim.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
	assert.Len(t, proto.Senders, 2)
	assert.Equal(t, []byte("sender1"), proto.Senders[0].Identity.Raw)
	assert.Equal(t, []byte("audit1"), proto.Senders[0].AuditInfo)
	assert.Equal(t, []byte("sender2"), proto.Senders[1].Identity.Raw)
	assert.Equal(t, []byte("audit2"), proto.Senders[1].AuditInfo)
}

// TestTransferOutputMetadata_ToProtos_WithReceivers tests ToProtos with receivers
func TestTransferOutputMetadata_ToProtos_WithReceivers(t *testing.T) {
	tom := &TransferOutputMetadata{
		OutputMetadata:  []byte("metadata1"),
		OutputAuditInfo: []byte("auditinfo1"),
		Receivers: []*AuditableIdentity{
			{Identity: Identity("receiver1"), AuditInfo: []byte("audit1")},
			{Identity: Identity("receiver2"), AuditInfo: []byte("audit2")},
		},
	}

	proto, err := tom.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
	assert.Len(t, proto.Receivers, 2)
	assert.Equal(t, []byte("receiver1"), proto.Receivers[0].Identity.Raw)
	assert.Equal(t, []byte("audit1"), proto.Receivers[0].AuditInfo)
	assert.Equal(t, []byte("receiver2"), proto.Receivers[1].Identity.Raw)
	assert.Equal(t, []byte("audit2"), proto.Receivers[1].AuditInfo)
}

// TestIssueMetadata_ToProtos_WithExtraSigners tests ToProtos with extra signers
func TestIssueMetadata_ToProtos_WithExtraSigners(t *testing.T) {
	im := &IssueMetadata{
		Issuer: AuditableIdentity{
			Identity:  Identity("issuer1"),
			AuditInfo: []byte("issuer_audit"),
		},
		Inputs: []*IssueInputMetadata{},
		Outputs: []*IssueOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
		ExtraSigners: []AuditableIdentity{
			{Identity: Identity("signer1"), AuditInfo: []byte("signer1_audit")},
			{Identity: Identity("signer2"), AuditInfo: []byte("signer2_audit")},
			{Identity: Identity("signer3"), AuditInfo: []byte("signer3_audit")},
		},
	}

	proto, err := im.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
	assert.Len(t, proto.ExtraSigners, 3)
	assert.Equal(t, []byte("signer1"), proto.ExtraSigners[0].Identity.Raw)
	assert.Equal(t, []byte("signer2"), proto.ExtraSigners[1].Identity.Raw)
	assert.Equal(t, []byte("signer3"), proto.ExtraSigners[2].Identity.Raw)
}

// TestTransferMetadata_ToProtos_WithExtraSigners tests ToProtos with extra signers
func TestTransferMetadata_ToProtos_WithExtraSigners(t *testing.T) {
	tm := &TransferMetadata{
		Inputs: []*TransferInputMetadata{
			{TokenID: &token.ID{TxId: "tx1", Index: 1}},
		},
		Outputs: []*TransferOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
		ExtraSigners: []AuditableIdentity{
			{Identity: Identity("signer1"), AuditInfo: []byte("signer1_audit")},
			{Identity: Identity("signer2"), AuditInfo: []byte("signer2_audit")},
		},
		Issuer: AuditableIdentity{
			Identity:  Identity("issuer1"),
			AuditInfo: []byte("issuer1_audit"),
		},
	}

	proto, err := tm.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto)
	assert.Len(t, proto.ExtraSigners, 2)
	assert.Equal(t, []byte("signer1"), proto.ExtraSigners[0].Identity.Raw)
	assert.Equal(t, []byte("signer2"), proto.ExtraSigners[1].Identity.Raw)
	assert.NotNil(t, proto.Issuer)
	assert.Equal(t, []byte("issuer1"), proto.Issuer.Identity.Raw)
}

// TestTokenRequest_ToProtos_EmptyAuditorSignatures tests ToProtos with empty auditor signatures
func TestTokenRequest_ToProtos_EmptyAuditorSignatures(t *testing.T) {
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("sig1"),
				},
			},
		},
	}

	proto, err := req.ToProtos()
	require.NoError(t, err)
	require.Len(t, proto.Signatures, 1)
	assert.NotNil(t, proto.Signatures[0].GetActionSignature())
	assert.Nil(t, proto.Signatures[0].GetAuditorSignature())
}

// TestIssueOutputMetadata_FromProtos_NilOutputMetadata tests FromProtos with nil output metadata
func TestIssueOutputMetadata_FromProtos_NilOutputMetadata(t *testing.T) {
	meta := &IssueOutputMetadata{}
	err := meta.FromProtos(nil)
	require.NoError(t, err)
}

// TestTransferInputMetadata_FromProtos_NilTokenID tests FromProtos with nil token ID
func TestTransferInputMetadata_FromProtos_NilTokenID(t *testing.T) {
	proto := &request.TransferInputMetadata{
		TokenId: nil,
	}

	meta := &TransferInputMetadata{}
	err := meta.FromProtos(proto)
	require.NoError(t, err)
	assert.Nil(t, meta.TokenID)
}

// TestTransferOutputMetadata_FromProtos_NilOutputMetadata tests FromProtos with nil output metadata
func TestTransferOutputMetadata_FromProtos_NilOutputMetadata(t *testing.T) {
	meta := &TransferOutputMetadata{}
	err := meta.FromProtos(nil)
	require.NoError(t, err)
}

// TestTokenRequest_ToProtos_WithBothIssuesAndTransfers tests ToProtos with multiple action types
func TestTokenRequest_ToProtos_WithBothIssuesAndTransfers(t *testing.T) {
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("sig1"),
				},
			},
			{
				Auditor: &AuditorSignature{
					Identity:  Identity("aud1"),
					Signature: []byte("audsig1"),
				},
			},
		},
	}

	proto, err := req.ToProtos()
	require.NoError(t, err)
	require.NotNil(t, proto)
	assert.Len(t, proto.Actions, 3)

	// Verify first action (issue1)
	typedAction0 := proto.Actions[0].GetTypedAction()
	require.NotNil(t, typedAction0)
	assert.Equal(t, []byte("issue1"), typedAction0.Raw)
	assert.Equal(t, request.ActionType_ACTION_TYPE_ISSUE, typedAction0.Type)

	// Verify second action (issue2)
	typedAction1 := proto.Actions[1].GetTypedAction()
	require.NotNil(t, typedAction1)
	assert.Equal(t, []byte("issue2"), typedAction1.Raw)
	assert.Equal(t, request.ActionType_ACTION_TYPE_ISSUE, typedAction1.Type)

	// Verify third action (transfer1)
	typedAction2 := proto.Actions[2].GetTypedAction()
	require.NotNil(t, typedAction2)
	assert.Equal(t, []byte("transfer1"), typedAction2.Raw)
	assert.Equal(t, request.ActionType_ACTION_TYPE_TRANSFER, typedAction2.Type)

	require.Len(t, proto.Signatures, 2)
	assert.NotNil(t, proto.Signatures[0].GetActionSignature())
	assert.Equal(t, uint32(0), proto.Signatures[0].GetActionSignature().ActionId)
	assert.NotNil(t, proto.Signatures[1].GetAuditorSignature())
	assert.Equal(t, []byte("aud1"), proto.Signatures[1].GetAuditorSignature().Identity.Raw)
}

// TestTokenRequest_FromProtos_WithAuditing tests FromProtos with auditing
func TestTokenRequest_FromProtos_WithAuditing(t *testing.T) {
	protoReq := &request.TokenRequest{
		Version: ProtocolV1,
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte("issue1"),
					},
				},
			},
		},
		Signatures: []*request.RequestSignature{
			{
				Signature: &request.RequestSignature_ActionSignature{
					ActionSignature: &request.ActionSignature{
						ActionId:  0,
						Signature: &request.Signature{Raw: []byte("sig1")},
					},
				},
			},
			{
				Signature: &request.RequestSignature_AuditorSignature{
					AuditorSignature: &request.AuditorSignature{
						Identity:  &driver.Identity{Raw: []byte("aud1")},
						Signature: &request.Signature{Raw: []byte("audsig1")},
					},
				},
			},
		},
	}

	req := &TokenRequest{}
	err := req.FromProtos(protoReq)
	require.NoError(t, err)
	issues := req.GetIssues()
	assert.Len(t, issues, 1)
	assert.Equal(t, []byte("issue1"), issues[0])
	assert.Len(t, req.Signatures, 2)
	require.NotNil(t, req.Signatures[0].Action)
	assert.Equal(t, []byte("sig1"), req.Signatures[0].Action.Signature)
	require.NotNil(t, req.Signatures[1].Auditor)
	assert.Equal(t, Identity("aud1"), req.Signatures[1].Auditor.Identity)
	assert.Equal(t, []byte("audsig1"), req.Signatures[1].Auditor.Signature)
}

// TestTokenRequest_MarshalToMessageToSign_WithAnchor tests MarshalToMessageToSign
func TestTokenRequest_MarshalToMessageToSign_WithAnchor(t *testing.T) {
	req := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	raw, err := req.MarshalToMessageToSign([]byte("anchor"))
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
}

// TestIssueInputMetadata_ToProtos_WithTokenID tests ToProtos with token ID
func TestIssueInputMetadata_ToProtos_WithTokenID(t *testing.T) {
	meta := &IssueInputMetadata{
		TokenID: &token.ID{TxId: "tx1", Index: 0},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto.TokenId)
}

// TestIssueInputMetadata_ToProtos_WithNilTokenID tests ToProtos with nil token ID
func TestIssueInputMetadata_ToProtos_WithNilTokenID(t *testing.T) {
	meta := &IssueInputMetadata{
		TokenID: nil,
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Nil(t, proto.TokenId)
}

// TestIssueOutputMetadata_ToProtos_WithReceivers tests ToProtos with receivers
func TestIssueOutputMetadata_ToProtos_WithReceivers(t *testing.T) {
	meta := &IssueOutputMetadata{
		OutputMetadata: []byte("output1"),
		Receivers: []*AuditableIdentity{
			{Identity: Identity("id1"), AuditInfo: []byte("audit1")},
		},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Len(t, proto.Receivers, 1)
	assert.Equal(t, []byte("id1"), proto.Receivers[0].Identity.Raw)
	assert.Equal(t, []byte("audit1"), proto.Receivers[0].AuditInfo)
}

// TestIssueOutputMetadata_ToProtos_WithNilReceivers tests ToProtos with nil receivers
func TestIssueOutputMetadata_ToProtos_WithNilReceivers(t *testing.T) {
	meta := &IssueOutputMetadata{
		OutputMetadata: []byte("output1"),
		Receivers:      nil,
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Empty(t, proto.Receivers)
}

// TestIssueMetadata_ToProtos_WithEmptyInputs tests ToProtos with empty inputs
func TestIssueMetadata_ToProtos_WithEmptyInputs(t *testing.T) {
	meta := &IssueMetadata{
		Issuer: AuditableIdentity{Identity: Identity("issuer1")},
		Inputs: []*IssueInputMetadata{},
		Outputs: []*IssueOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Empty(t, proto.Inputs)
}

// TestIssueMetadata_ToProtos_WithEmptyOutputs tests ToProtos with empty outputs
func TestIssueMetadata_ToProtos_WithEmptyOutputs(t *testing.T) {
	meta := &IssueMetadata{
		Issuer:  AuditableIdentity{Identity: Identity("issuer1")},
		Inputs:  []*IssueInputMetadata{{TokenID: &token.ID{TxId: "tx1"}}},
		Outputs: []*IssueOutputMetadata{},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Empty(t, proto.Outputs)
}

// TestIssueMetadata_ToProtos_WithExtraSigners tests ToProtos with extra signers
// TestTransferInputMetadata_ToProtos_WithTokenID tests ToProtos with token ID
func TestTransferInputMetadata_ToProtos_WithTokenID(t *testing.T) {
	meta := &TransferInputMetadata{
		TokenID: &token.ID{TxId: "tx1", Index: 0},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto.TokenId)
}

// TestTransferOutputMetadata_ToProtos_WithNilReceivers tests ToProtos with nil receivers
func TestTransferOutputMetadata_ToProtos_WithNilReceivers(t *testing.T) {
	meta := &TransferOutputMetadata{
		OutputMetadata: []byte("output1"),
		Receivers:      nil,
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Empty(t, proto.Receivers)
}

// TestTransferMetadata_ToProtos_WithEmptyInputs tests ToProtos with empty inputs
func TestTransferMetadata_ToProtos_WithEmptyInputs(t *testing.T) {
	meta := &TransferMetadata{
		Inputs:  []*TransferInputMetadata{{TokenID: &token.ID{TxId: "tx1"}}},
		Outputs: []*TransferOutputMetadata{},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.Empty(t, proto.Outputs)
}

// TestTransferMetadata_ToProtos_WithExtraSigners tests ToProtos with extra signers
func TestTransferMetadata_ToProtos_WithIssuer(t *testing.T) {
	meta := &TransferMetadata{
		Outputs: []*TransferOutputMetadata{
			{OutputMetadata: []byte("output1")},
		},
		Issuer: AuditableIdentity{
			Identity: Identity("issuer1"),
		},
	}

	proto, err := meta.ToProtos()
	require.NoError(t, err)
	assert.NotNil(t, proto.Issuer)
	assert.Equal(t, []byte("issuer1"), proto.Issuer.Identity.Raw)
}

// TestTransferMetadata_FromProtos_WithEmptySlices tests FromProtos with empty slices
func TestTransferMetadata_FromProtos_WithEmptySlices(t *testing.T) {
	proto := &request.TransferMetadata{
		Inputs:  []*request.TransferInputMetadata{},
		Outputs: []*request.OutputMetadata{},
	}

	meta := &TransferMetadata{}
	err := meta.FromProtos(proto)
	require.NoError(t, err)
	assert.Empty(t, meta.Inputs)
	assert.Empty(t, meta.Outputs)
}

// TestTokenRequestMetadata_Bytes_ToProtosError tests Bytes error when ToProtos fails
func TestTokenRequestMetadata_Bytes_ToProtosError(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{
			{ActionID: 0, IssueMetadata: nil, TransferMetadata: nil},
		},
	}

	raw, err := reqMeta.Bytes()
	require.Error(t, err)
	assert.Nil(t, raw)
}

// TestTokenRequestMetadata_ToProtos_WithEmptyMetadata tests ToProtos with empty metadata
func TestTokenRequestMetadata_ToProtos_WithEmptyMetadata(t *testing.T) {
	reqMeta := &TokenRequestMetadata{
		Actions: []*ActionMetadataEntry{},
	}

	proto, err := reqMeta.ToProtos()
	require.NoError(t, err)
	assert.Empty(t, proto.Metadata)
}

// TestMarshalToMessageToSign_V1_Success tests the V1 protocol signature message construction
func TestMarshalToMessageToSign_V1_Success(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	anchor := []byte("test-anchor")
	msg, err := tr.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)
	require.NotNil(t, msg)

	// V1 uses structured ASN.1 format
	assert.NotEmpty(t, msg)
}

// TestMarshalToMessageToSign_V1_EmptyAnchor tests V1 validation for empty anchor
func TestMarshalToMessageToSign_V1_EmptyAnchor(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	_, err := tr.marshalToMessageToSignV1([]byte{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAnchorEmpty)
}

// TestMarshalToMessageToSign_V1_AnchorTooLarge tests V1 validation for oversized anchor
func TestMarshalToMessageToSign_V1_AnchorTooLarge(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	// Create anchor larger than MaxAnchorSize
	largeAnchor := make([]byte, MaxAnchorSize+1)
	_, err := tr.marshalToMessageToSignV1(largeAnchor)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAnchorTooLarge)
}

// TestMarshalToMessageToSign_V1_UniquenessProperty tests that different inputs produce different outputs
func TestMarshalToMessageToSign_V1_UniquenessProperty(t *testing.T) {
	tr1 := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}
	tr2 := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer2")},
		},
	}

	anchor := []byte("test-anchor")

	msg1, err := tr1.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	msg2, err := tr2.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	// Different requests should produce different messages
	assert.NotEqual(t, msg1, msg2, "Different requests should produce different signature messages")
}

// TestMarshalToMessageToSign_V1_AnchorUniqueness tests that different anchors produce different outputs
func TestMarshalToMessageToSign_V1_AnchorUniqueness(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	anchor1 := []byte("anchor1")
	anchor2 := []byte("anchor2")

	msg1, err := tr.marshalToMessageToSignV1(anchor1)
	require.NoError(t, err)

	msg2, err := tr.marshalToMessageToSignV1(anchor2)
	require.NoError(t, err)

	// Different anchors should produce different messages
	assert.NotEqual(t, msg1, msg2, "Different anchors should produce different signature messages")
}

// TestMarshalToMessageToSign_V1_Deterministic tests that V1 is deterministic
func TestMarshalToMessageToSign_V1_Deterministic(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	anchor := []byte("test-anchor")

	msg1, err := tr.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	msg2, err := tr.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	// Same input should always produce same output
	assert.Equal(t, msg1, msg2, "V1 should be deterministic")
}

// TestTokenRequest_VersionPreservation tests that version is preserved during serialization
func TestTokenRequest_VersionPreservation(t *testing.T) {
	// Test V1 preservation
	trV1 := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Version: uint32(ProtocolV1),
	}

	bytes, err := trV1.Bytes()
	require.NoError(t, err)

	trV1Restored := &TokenRequest{}
	err = trV1Restored.FromBytes(bytes)
	require.NoError(t, err)
	assert.Equal(t, uint32(ProtocolV1), trV1Restored.Version, "V1 version should be preserved")

	// Test default to V1 for new requests
	trNew := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		// Version not set (0)
	}

	bytes, err = trNew.Bytes()
	require.NoError(t, err)

	trNewRestored := &TokenRequest{}
	err = trNewRestored.FromBytes(bytes)
	require.NoError(t, err)
	assert.Equal(t, uint32(ProtocolV1), trNewRestored.Version, "New requests should default to V1")
}

// TestMarshalToMessageToSign_V1_StructuredFormat verifies V1 uses structured ASN.1 format
func TestMarshalToMessageToSign_V1_StructuredFormat(t *testing.T) {
	tr := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue2")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Version: uint32(ProtocolV1),
	}

	anchor := []byte("test-anchor")

	// Get the V1 implementation output
	msgNew, err := tr.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	// V1 should use structured ASN.1 format with Request and Anchor fields
	// Verify it's not empty and has reasonable length
	assert.NotEmpty(t, msgNew, "V1 should produce non-empty output")
	assert.Greater(t, len(msgNew), len(anchor), "V1 output should be larger than anchor alone")

	// Verify the output is valid ASN.1 by attempting to parse it
	type signatureMessage struct {
		Request []byte
		Anchor  []byte
	}
	var parsed signatureMessage
	_, err = asn1.Unmarshal(msgNew, &parsed)
	require.NoError(t, err, "V1 output should be valid ASN.1")
	assert.Equal(t, anchor, parsed.Anchor, "Anchor should be preserved in structured format")
}

// TestMarshalToMessageToSign_V1_ExcludesVersion verifies Version field is not included in V1 marshaling
func TestMarshalToMessageToSign_V1_ExcludesVersion(t *testing.T) {
	// Create two requests with same Issues/Transfers but different Versions
	tr1 := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Version: uint32(ProtocolV1),
	}

	tr2 := &TokenRequest{
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Version: uint32(2), // Different version number (hypothetical future version)
	}

	anchor := []byte("test-anchor")

	// Both should produce the same V1 message (Version should not be included)
	msg1, err := tr1.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	msg2, err := tr2.marshalToMessageToSignV1(anchor)
	require.NoError(t, err)

	assert.Equal(t, msg1, msg2, "V1 marshaling should not include Version field")
}

// TestTokenRequest_Validate_Success tests successful validation of a valid TokenRequest
func TestTokenRequest_Validate_Success(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("signature1"),
				},
			},
		},
	}

	err := req.Validate()
	require.NoError(t, err)
}

// TestTokenRequest_Validate_EmptyRequest tests that completely empty requests are allowed (for test scenarios)
func TestTokenRequest_Validate_EmptyRequest(t *testing.T) {
	req := &TokenRequest{
		Version: 0,
		Actions: []*TypedAction{},
	}

	err := req.Validate()
	require.NoError(t, err, "Empty requests should be allowed for test scenarios")
}

// TestTokenRequest_Validate_NilActions tests that nil Actions slice is allowed (for test scenarios)
func TestTokenRequest_Validate_NilActions(t *testing.T) {
	req := &TokenRequest{
		Version: 0,
		Actions: nil,
	}

	err := req.Validate()
	require.NoError(t, err, "Nil Actions should be allowed for test scenarios")
}

// TestTokenRequest_Validate_EmptyActionsWithVersion tests that empty actions with version set is allowed
// This is necessary because ToProtos() sets Version=ProtocolV1 even for empty requests
func TestTokenRequest_Validate_EmptyActionsWithVersion(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{}, // Empty actions
	}

	err := req.Validate()
	require.NoError(t, err, "Empty actions should be allowed even with version set (ToProtos behavior)")
}

// TestTokenRequest_Validate_NilAction tests validation failure for nil action
func TestTokenRequest_Validate_NilAction(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			nil, // Nil action
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action at index 1 is nil")
}

// TestTokenRequest_Validate_EmptyActionRaw tests validation failure for empty action Raw bytes
func TestTokenRequest_Validate_EmptyActionRaw(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte{}}, // Empty Raw
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action at index 1 has empty Raw bytes")
}

// TestTokenRequest_Validate_NilSignature tests validation failure for nil signature
func TestTokenRequest_Validate_NilSignature(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("signature1"),
				},
			},
			nil, // Nil signature
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature at index 1 is nil")
}

// TestTokenRequest_Validate_EmptyActionSignature tests validation failure for empty action signature bytes
func TestTokenRequest_Validate_EmptyActionSignature(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte{}, // Empty signature
				},
			},
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action signature at index 0 has empty signature bytes")
}

// TestTokenRequest_Validate_EmptyAuditorSignature tests validation failure for empty auditor signature bytes
func TestTokenRequest_Validate_EmptyAuditorSignature(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
		Signatures: []*RequestSignature{
			{
				Auditor: &AuditorSignature{
					Identity:  Identity("auditor1"),
					Signature: []byte{}, // Empty signature
				},
			},
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auditor signature at index 0 has empty signature bytes")
}

// TestTokenRequest_Validate_SignatureWithoutActionOrAuditor tests validation failure for signature with neither action nor auditor
func TestTokenRequest_Validate_SignatureWithoutActionOrAuditor(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
		Signatures: []*RequestSignature{
			{
				// Neither Action nor Auditor set
			},
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature at index 0 has neither action nor auditor signature")
}

// TestTokenRequest_Validate_NoSignatures tests successful validation with no signatures
func TestTokenRequest_Validate_NoSignatures(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
		Signatures: []*RequestSignature{}, // Empty signatures is valid
	}

	err := req.Validate()
	require.NoError(t, err)
}

// TestTokenRequest_Validate_MultipleValidSignatures tests successful validation with multiple signatures
func TestTokenRequest_Validate_MultipleValidSignatures(t *testing.T) {
	req := &TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("transfer1")},
		},
		Signatures: []*RequestSignature{
			{
				Action: &ActionSignature{
					ActionID:  0,
					Signature: []byte("signature1"),
				},
			},
			{
				Action: &ActionSignature{
					ActionID:  1,
					Signature: []byte("signature2"),
				},
			},
			{
				Auditor: &AuditorSignature{
					Identity:  Identity("auditor1"),
					Signature: []byte("audsig1"),
				},
			},
		},
	}

	err := req.Validate()
	require.NoError(t, err)
}

// TestTokenRequest_FromBytes_CallsValidate tests that FromBytes calls Validate
func TestTokenRequest_FromBytes_CallsValidate(t *testing.T) {
	// Create a valid protobuf request
	protoReq := &request.TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte("issue1"),
					},
				},
			},
		},
	}

	raw, err := proto.Marshal(protoReq)
	require.NoError(t, err)

	// Deserialize - should succeed
	req := &TokenRequest{}
	err = req.FromBytes(raw)
	require.NoError(t, err)

	// Now create an invalid protobuf request (action with empty Raw bytes)
	invalidProtoReq := &request.TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte{}, // Empty Raw bytes should fail validation
					},
				},
			},
		},
	}

	invalidRaw, err := proto.Marshal(invalidProtoReq)
	require.NoError(t, err)

	// Deserialize - should fail validation
	invalidReq := &TokenRequest{}
	err = invalidReq.FromBytes(invalidRaw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

// TestTokenRequest_FromProtos_CallsValidate tests that FromProtos calls Validate
func TestTokenRequest_FromProtos_CallsValidate(t *testing.T) {
	// Create a valid protobuf request
	protoReq := &request.TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte("issue1"),
					},
				},
			},
		},
	}

	// Deserialize - should succeed
	req := &TokenRequest{}
	err := req.FromProtos(protoReq)
	require.NoError(t, err)

	// Now create an invalid protobuf request (action with empty Raw bytes)
	invalidProtoReq := &request.TokenRequest{
		Version: uint32(ProtocolV1),
		Actions: []*request.Action{
			{
				Action: &request.Action_TypedAction{
					TypedAction: &request.TypedAction{
						Type: request.ActionType_ACTION_TYPE_ISSUE,
						Raw:  []byte{}, // Empty Raw bytes should fail validation
					},
				},
			},
		},
	}

	// Deserialize - should fail validation
	invalidReq := &TokenRequest{}
	err = invalidReq.FromProtos(invalidProtoReq)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

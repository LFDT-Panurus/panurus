/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildInputSenderAuditContext returns an honest two-input transfer: each input's metadata
// names one sender equal to the token owner, with that owner's audit info.
func buildInputSenderAuditContext(t *testing.T) (*AuditContext, *mock.Deserializer) {
	t.Helper()

	des := &mock.Deserializer{}
	action := &actions.TransferAction{
		Outputs: []*actions.Output{{Owner: []byte("carol"), Type: "USD", Quantity: "0x2"}},
	}
	metadata := &driver.TransferMetadata{Outputs: []*driver.TransferOutputMetadata{{}}}
	auditTokens := map[string]*token.Token{}
	for i, owner := range []string{"alice", "bob"} {
		id := &token.ID{TxId: "tx1", Index: uint64(i)}
		tok := &token.Token{Owner: []byte(owner), Type: "USD", Quantity: "0x1"}
		auditTokens[id.String()] = tok
		action.Inputs = append(action.Inputs, &actions.TransferActionInput{
			ID:    id,
			Input: &actions.Output{Owner: tok.Owner, Type: tok.Type, Quantity: tok.Quantity},
		})
		metadata.Inputs = append(metadata.Inputs, &driver.TransferInputMetadata{
			TokenID: id,
			Senders: []*driver.AuditableIdentity{{Identity: tok.Owner, AuditInfo: []byte(owner + "-audit-info")}},
		})
	}

	return &AuditContext{
		PP:                   &setup.PublicParams{QuantityPrecision: 64},
		TokenRequestMetadata: &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{TransferMetadata: metadata}}},
		Deserializer:         des,
		AuditTokens:          auditTokens,
		TransferAction:       action,
	}, des
}

// TestTransferAuditValidate_InputSenderBinding covers #2842: the sender audit info of a spent
// input must be bound to the spent token's owner before the auditor accepts the transfer.
func TestTransferAuditValidate_InputSenderBinding(t *testing.T) {
	inputMD := func(c *AuditContext, i int) *driver.TransferInputMetadata {
		return c.TokenRequestMetadata.Actions[0].TransferMetadata.Inputs[i]
	}

	tests := []struct {
		name    string
		mutate  func(c *AuditContext, des *mock.Deserializer)
		wantErr string
	}{
		{
			name:   "honest inputs are accepted",
			mutate: func(*AuditContext, *mock.Deserializer) {},
		},
		{
			name: "sender that is not the token owner is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				inputMD(c, 0).Senders[0] = &driver.AuditableIdentity{Identity: []byte("mallory"), AuditInfo: []byte("mallory-audit-info")}
			},
			wantErr: "sender identity at index [0] does not match token owner",
		},
		{
			name: "audit info that does not match the owner is rejected",
			mutate: func(_ *AuditContext, des *mock.Deserializer) {
				des.MatchIdentityReturns(errors.New("no match"))
			},
			wantErr: "sender audit info at index [0] does not match token owner",
		},
		{
			name: "zero senders are rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				inputMD(c, 0).Senders = nil
			},
			wantErr: "must have exactly one sender, found [0]",
		},
		{
			name: "two senders are rejected even if the first is the owner",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				md := inputMD(c, 0)
				md.Senders = append(md.Senders, &driver.AuditableIdentity{Identity: []byte("mallory"), AuditInfo: []byte("mallory-audit-info")})
			},
			wantErr: "must have exactly one sender, found [2]",
		},
		{
			name: "nil sender is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				inputMD(c, 0).Senders[0] = nil
			},
			wantErr: "sender at index [0] is nil",
		},
		{
			name: "empty sender identity is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				inputMD(c, 0).Senders[0].Identity = nil
			},
			wantErr: "sender identity at index [0] does not match token owner",
		},
		{
			name: "empty audit info is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				inputMD(c, 0).Senders[0].AuditInfo = nil
			},
			wantErr: "sender at index [0] has empty audit info",
		},
		{
			name: "metadata token ID that differs from the action input ID is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				// Both IDs exist in the audit tokens, so only the ID binding catches it.
				inputMD(c, 0).TokenID = &token.ID{TxId: "tx1", Index: 1}
			},
			wantErr: "token ID mismatch at input [0]",
		},
		{
			name: "action owner and sender that differ from the stored token owner are rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				victim := []byte("victim")
				c.TransferAction.Inputs[0].Input.Owner = victim
				inputMD(c, 0).Senders[0] = &driver.AuditableIdentity{Identity: victim, AuditInfo: []byte("victim-audit-info")}
			},
			wantErr: "token owner at index [0] does not match the stored token owner",
		},
		{
			name: "input type that differs from the stored token type is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				c.TransferAction.Inputs[0].Input.Type = "EUR"
			},
			wantErr: "token type at index [0] does not match the stored token type",
		},
		{
			name: "input quantity that differs from the stored token quantity is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				c.TransferAction.Inputs[0].Input.Quantity = "0x64"
			},
			wantErr: "token quantity at index [0] does not match the stored token quantity",
		},
		{
			name: "same quantity in a different encoding is accepted",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				c.TransferAction.Inputs[0].Input.Quantity = "1"
			},
		},
		{
			name: "invalid input quantity is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				c.TransferAction.Inputs[0].Input.Quantity = "not-a-number"
			},
			wantErr: "invalid quantity for input at index [0]",
		},
		{
			name: "invalid stored quantity is rejected",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				c.AuditTokens[c.TransferAction.Inputs[0].ID.String()].Quantity = "not-a-number"
				c.TransferAction.Inputs[0].Input.Quantity = "0x1"
			},
			wantErr: "invalid stored quantity for input at index [0]",
		},
		{
			name: "nil deserializer returns an error",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				c.Deserializer = nil
			},
			wantErr: "deserializer is nil",
		},
		{
			name: "bad second input is reported at index 1",
			mutate: func(c *AuditContext, _ *mock.Deserializer) {
				inputMD(c, 1).Senders[0].Identity = []byte("mallory")
			},
			wantErr: "sender identity at index [1] does not match token owner",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, des := buildInputSenderAuditContext(t)
			tc.mutate(c, des)

			err := TransferAuditValidate()(t.Context(), c)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, des.MatchIdentityCallCount())
			for i, owner := range []string{"alice", "bob"} {
				_, id, ai := des.MatchIdentityArgsForCall(i)
				assert.Equal(t, driver.Identity(owner), id)
				assert.Equal(t, []byte(owner+"-audit-info"), ai)
			}
		})
	}
}

// TestValidateTransferInputs_MalformedInput checks that the helper rejects malformed input
// without panicking, independently of the checks that run before it.
func TestValidateTransferInputs_MalformedInput(t *testing.T) {
	des := &mock.Deserializer{}
	id := &token.ID{TxId: "tx1"}
	ctx := t.Context()

	err := validateTransferInputs(ctx, des, []*actions.TransferActionInput{nil}, []*driver.TransferInputMetadata{{TokenID: id}}, nil, 64)
	require.ErrorContains(t, err, "input at index [0] is nil")

	in := []*actions.TransferActionInput{{ID: id, Input: &actions.Output{Owner: []byte("alice")}}}
	err = validateTransferInputs(ctx, des, in, []*driver.TransferInputMetadata{nil}, nil, 64)
	require.ErrorContains(t, err, "metadata for input at index [0] is nil")

	err = validateTransferInputs(ctx, des, in, nil, nil, 64)
	require.ErrorContains(t, err, "expected [1] input metadata, got [0]")

	// An empty owner with an empty sender identity passes bytes.Equal, so it needs its own check.
	emptyOwner := []*actions.TransferActionInput{{ID: id, Input: &actions.Output{}}}
	emptySender := []*driver.TransferInputMetadata{{TokenID: id, Senders: []*driver.AuditableIdentity{{AuditInfo: []byte("x")}}}}
	err = validateTransferInputs(ctx, des, emptyOwner, emptySender, nil, 64)
	require.ErrorContains(t, err, "input at index [0] has an empty owner")

	md := []*driver.TransferInputMetadata{{TokenID: id, Senders: []*driver.AuditableIdentity{{Identity: []byte("alice"), AuditInfo: []byte("x")}}}}
	err = validateTransferInputs(ctx, des, in, md, nil, 64)
	require.ErrorContains(t, err, "input token at index [0] not found in audit tokens")
	err = validateTransferInputs(ctx, des, in, md, map[string]*token.Token{id.String(): nil}, 64)
	require.ErrorContains(t, err, "input token at index [0] not found in audit tokens")
	assert.Zero(t, des.MatchIdentityCallCount())
}

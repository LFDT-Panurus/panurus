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
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// buildRedeemAuditContext assembles the minimal AuditContext needed to exercise
// TransferAuditValidate's redeem-issuer membership check: a single input, a single
// redeem (empty-owner) output, and transfer metadata whose Issuer matches the
// action's. issuers is what the public parameters list as authorized; approvingIssuer
// is who actually signed the redeem as approver.
func buildRedeemAuditContext(t *testing.T, issuers []driver.Identity, approvingIssuer driver.Identity) *AuditContext {
	t.Helper()

	tokenID := &token.ID{TxId: "tx1", Index: 0}
	inputToken := &token.Token{Owner: []byte("alice"), Type: "USD", Quantity: "0x64"}

	action := &actions.TransferAction{
		Inputs: []*actions.TransferActionInput{
			{ID: tokenID, Input: &actions.Output{Owner: inputToken.Owner, Type: inputToken.Type, Quantity: inputToken.Quantity}},
		},
		Outputs: []*actions.Output{
			{Owner: nil, Type: "USD", Quantity: "0x64"}, // redeem output: empty owner
		},
		Issuer: approvingIssuer,
	}

	metadata := &driver.TransferMetadata{
		Inputs: []*driver.TransferInputMetadata{
			{TokenID: tokenID},
		},
		Outputs: []*driver.TransferOutputMetadata{
			{},
		},
		Issuer: driver.AuditableIdentity{Identity: approvingIssuer},
	}

	pp := &setup.PublicParams{QuantityPrecision: 64}
	pp.SetIssuers(issuers)

	return &AuditContext{
		PP:                   pp,
		TokenRequestMetadata: &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{ActionID: 0, TransferMetadata: metadata}}},
		AuditTokens:          map[string]*token.Token{tokenID.String(): inputToken},
		TransferAction:       action,
		ActionIndex:          0,
	}
}

// TestTransferAuditValidate_RedeemIssuerMembership pins the fabtoken audit-time
// counterpart to the chaincode validator's membership check added for
// GHSA-7c26-8c2v-wqj2 (TransferSignatureValidate in validator_transfer.go) and to
// zkatdlog's existing audit-time check (validateRedeemIssuer in
// token/core/zkatdlog/nogh/v1/audit/auditor.go). Without it, fabtoken's auditor
// signs and records a Pending audit entry for a redeem approved by an unrecognized
// issuer before the transaction ever reaches the chaincode, so the eventual
// on-chain rejection leaves that Pending record to be reconciled only once the
// recovery manager's NotFoundGracePeriod elapses.
func TestTransferAuditValidate_RedeemIssuerMembership(t *testing.T) {
	validate := TransferAuditValidate(64)

	t.Run("redeem approved by a recognized issuer is accepted", func(t *testing.T) {
		issuer := driver.Identity("issuer")
		ctx := buildRedeemAuditContext(t, []driver.Identity{issuer}, issuer)

		err := validate(t.Context(), ctx)
		require.NoError(t, err)
	})

	t.Run("redeem approved by an unrecognized issuer is rejected", func(t *testing.T) {
		issuer := driver.Identity("issuer")
		unauthorized := driver.Identity("unauthorized-issuer")
		ctx := buildRedeemAuditContext(t, []driver.Identity{issuer}, unauthorized)

		err := validate(t.Context(), ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "is not a recognized issuer")
	})

	t.Run("redeem with no issuer at all is rejected when issuers are configured", func(t *testing.T) {
		issuer := driver.Identity("issuer")
		ctx := buildRedeemAuditContext(t, []driver.Identity{issuer}, nil)

		err := validate(t.Context(), ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have at least one issuer")
	})

	t.Run("redeem is accepted without issuer membership check when public params list no issuers", func(t *testing.T) {
		unauthorized := driver.Identity("unauthorized-issuer")
		ctx := buildRedeemAuditContext(t, nil, unauthorized)

		err := validate(t.Context(), ctx)
		require.NoError(t, err)
	})
}

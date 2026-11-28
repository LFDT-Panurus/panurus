/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/audit"
	fabdriver "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/driver"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// x509Owner loads the x509 identity in the MSP folder at path and returns it wrapped as a typed
// owner, with the audit info an honest wallet attaches to it.
func x509Owner(t *testing.T, path string) (driver.Identity, []byte) {
	t.Helper()

	km, _, err := x509.NewKeyManager(path, nil, x509.NewKeyStore(kvs.NewTrackedMemory()))
	require.NoError(t, err)
	desc, err := km.Identity(t.Context(), nil)
	require.NoError(t, err)
	owner, err := identity.WrapWithType(x509.IdentityType, desc.Identity)
	require.NoError(t, err)

	return owner, desc.AuditInfo
}

// spendContext returns an AuditContext for a one-input transfer of a token owned by owner,
// with the real fabtoken deserializer.
func spendContext(owner driver.Identity, auditInfo []byte) *audit.AuditContext {
	id := &token.ID{TxId: "tx1"}
	tok := &token.Token{Owner: owner, Type: "USD", Quantity: "0x1"}
	action := &actions.TransferAction{
		Inputs:  []*actions.TransferActionInput{{ID: id, Input: &actions.Output{Owner: owner, Type: "USD", Quantity: "0x1"}}},
		Outputs: []*actions.Output{{Owner: []byte("carol"), Type: "USD", Quantity: "0x1"}},
	}
	metadata := &driver.TransferMetadata{
		Inputs:  []*driver.TransferInputMetadata{{TokenID: id, Senders: []*driver.AuditableIdentity{{Identity: owner, AuditInfo: auditInfo}}}},
		Outputs: []*driver.TransferOutputMetadata{{}},
	}

	return &audit.AuditContext{
		PP:                   &setup.PublicParams{QuantityPrecision: 64},
		TokenRequestMetadata: &driver.TokenRequestMetadata{Actions: []*driver.ActionMetadataEntry{{TransferMetadata: metadata}}},
		Deserializer:         fabdriver.NewDeserializer(),
		AuditTokens:          map[string]*token.Token{id.String(): tok},
		TransferAction:       action,
	}
}

// TestTransferAuditValidate_InputSenderRealDeserializer checks that the real fabtoken
// matchers accept honest x509 and multisig input metadata, and reject a foreign EID.
func TestTransferAuditValidate_InputSenderRealDeserializer(t *testing.T) {
	validate := audit.TransferAuditValidate()
	owner, auditInfo := x509Owner(t, "../../../../services/identity/x509/testdata/msp1")

	t.Run("honest x509 owner is accepted", func(t *testing.T) {
		require.NoError(t, validate(t.Context(), spendContext(owner, auditInfo)))
	})

	t.Run("honest multisig owner is accepted", func(t *testing.T) {
		other, otherAuditInfo := x509Owner(t, "../../../../../cmd/tokengen/testdata/issuers/msp")
		msOwner, err := multisig.WrapIdentities(owner, other)
		require.NoError(t, err)
		msAuditInfo, err := multisig.WrapAuditInfo([][]byte{auditInfo, otherAuditInfo})
		require.NoError(t, err)
		require.NoError(t, validate(t.Context(), spendContext(msOwner, msAuditInfo)))
	})

	t.Run("x509 audit info naming another enrollment ID is rejected", func(t *testing.T) {
		foreign, err := (&x509.AuditInfo{EID: "mallory", RH: []byte("rh")}).Bytes()
		require.NoError(t, err)
		err = validate(t.Context(), spendContext(owner, foreign))
		require.ErrorContains(t, err, "sender audit info at index [0] does not match token owner")
	})
}

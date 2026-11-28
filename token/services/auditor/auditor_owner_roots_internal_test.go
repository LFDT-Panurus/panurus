/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An input owner that fails the x509 owner checks fails the audit instead of staying unattributed.
func TestCompleteInputsWithEmptyEID_UntrustedOwnerFailsAudit(t *testing.T) {
	root := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("owner-root"), nil)
	checker, err := x509.NewOwnerChainChecker([][]byte{root.PEM}, nil, nil)
	require.NoError(t, err)
	eidrh := deserializer.NewEIDRHDeserializer()
	eidrh.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{Checker: checker})

	owner := func(cert *crypto.TestCertificate) ([]byte, []byte) {
		id, err := identity.WrapWithType(x509.IdentityType, cert.PEM)
		require.NoError(t, err)
		rh, err := crypto.RevocationHandleFromCert(cert.Cert)
		require.NoError(t, err)
		info, err := (&x509.AuditInfo{EID: "victim", RH: rh}).Bytes()
		require.NoError(t, err)

		return id, info
	}
	audit := func(id, info []byte) (*token.AuditRecord, error) {
		tms, ws := newInternalTestManagementServiceWithTokens(t, []*token2.Token{{Type: "USD", Quantity: "100", Owner: id}})
		ws.GetEIDAndRHStub = eidrh.GetEIDAndRH
		rw := newRequestWrapper(token.NewRequest(tms, token.RequestAnchor("tx-owner-roots")), tms)
		record := &token.AuditRecord{
			Inputs: token.NewInputStream(nil, []*token.Input{{
				ActionType:     transferAction,
				Id:             &token2.ID{TxId: "123"},
				OwnerAuditInfo: info,
			}}, 0),
			Outputs: token.NewOutputStream([]*token.Output{{ActionType: transferAction, EnrollmentID: "target"}}, 0),
		}

		return record, rw.completeInputsWithEmptyEID(t.Context(), record)
	}

	record, err := audit(owner(crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("victim"), root)))
	require.NoError(t, err)
	assert.Equal(t, "victim", record.Inputs.At(0).EnrollmentID)

	_, err = audit(owner(crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("victim"), nil)))
	require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
	assert.Contains(t, err.Error(), "failed resolving enrollment id for input")
}

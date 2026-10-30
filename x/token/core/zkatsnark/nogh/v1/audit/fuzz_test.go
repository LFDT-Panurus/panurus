/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/audit"
)

// FuzzAuditorCheckNoPanic feeds attacker-controlled token request and
// metadata bytes to the auditor and checks it never panics. The audit tokens
// are served for every token ID listed in the metadata.
func FuzzAuditorCheckNoPanic(f *testing.F) {
	seed := func(fx *fixture) {
		tr, md := fx.request(f)
		rawTR, err := tr.Bytes()
		require.NoError(f, err)
		rawMD, err := md.Bytes()
		require.NoError(f, err)
		f.Add(rawTR, rawMD)
		f.Add(rawTR[:len(rawTR)/2], rawMD)
		f.Add(rawTR, rawMD[:len(rawMD)/2])
	}

	seed(newIssueFixture(f, ecc.BN254))
	seed(newTransferFixture(f, ecc.BN254))
	seed(newRedeemFixture(f, ecc.BN254))

	// metadata with an extra output
	fx := newTransferFixture(f, ecc.BN254)
	fx.transferMD.Outputs = append(fx.transferMD.Outputs, fx.transferMD.Outputs[0])
	seed(fx)
	// metadata with a missing output
	fx = newIssueFixture(f, ecc.BN254)
	fx.issueMD.Outputs = fx.issueMD.Outputs[:1]
	seed(fx)
	// input IDs shorter than inputs
	fx = newTransferFixture(f, ecc.BN254)
	fx.transfer.InputIDs = fx.transfer.InputIDs[:1]
	seed(fx)
	// a nil input ID
	fx = newTransferFixture(f, ecc.BN254)
	fx.transfer.InputIDs[0] = nil
	seed(fx)
	// a nil sender
	fx = newTransferFixture(f, ecc.BN254)
	fx.transferMD.Inputs[0].Senders = []*driver.AuditableIdentity{nil}
	seed(fx)

	f.Add([]byte{}, []byte{})
	f.Add([]byte{0x0a, 0x00}, []byte{0x0a, 0x00})

	p := newPP(ecc.BN254, issuer)
	auditor := audit.NewAuditor(logging.MustGetLogger(), noop.NewTracerProvider().Tracer("fuzz"), newMatcher(), p, driver.ResourceLimits{})

	f.Fuzz(func(t *testing.T, rawTR, rawMD []byte) {
		t.Helper()
		tr := &driver.TokenRequest{}
		if err := tr.FromBytes(rawTR); err != nil {
			return
		}
		md := &driver.TokenRequestMetadata{}
		if err := md.FromBytes(rawMD); err != nil {
			return
		}
		auditTokens := map[string]*token.Token{}
		for _, a := range md.Actions {
			if a == nil || a.TransferMetadata == nil {
				continue
			}
			for _, id := range a.TransferMetadata.TokenIDs() {
				if id != nil {
					auditTokens[id.String()] = &token.Token{Owner: alice, Type: "ABC", Quantity: "0x1e"}
				}
			}
		}
		_ = auditor.Check(t.Context(), tr, md, "tx", auditTokens)
	})
}

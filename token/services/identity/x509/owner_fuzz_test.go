/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package x509

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/stretchr/testify/require"
)

// FuzzAuditInfoDeserializerWithRootsNoPanic fuzzes the identity and audit info, both peer-supplied,
// through the checked AuditInfoDeserializer and AuditInfoMatcher.
// Whatever they accept is bound to a certificate issued by the owner root.
func FuzzAuditInfoDeserializerWithRootsNoPanic(f *testing.F) {
	root := crypto.NewTestCertificate(f, crypto.NewTestCATemplate("root"), nil)
	legit := crypto.NewTestCertificate(f, crypto.NewTestLeafTemplate("victim"), root)
	forged := crypto.NewTestCertificate(f, crypto.NewTestLeafTemplate("victim"), nil)
	checker, err := NewOwnerChainChecker([][]byte{root.PEM}, nil, nil)
	require.NoError(f, err)
	des := &AuditInfoDeserializer{Checker: checker}
	matchers := &AuditMatcherDeserializer{Checker: checker}

	auditInfo := func(c *crypto.TestCertificate) []byte {
		rh, err := crypto.RevocationHandleFromCert(c.Cert)
		require.NoError(f, err)
		raw, err := (&AuditInfo{EID: "victim", RH: rh}).Bytes()
		require.NoError(f, err)

		return raw
	}
	f.Add(legit.PEM, auditInfo(legit))
	f.Add(forged.PEM, auditInfo(forged))
	f.Add([]byte{}, []byte{})
	f.Add(legit.PEM[:len(legit.PEM)/2], []byte(`{"EID":"victim"}`))
	f.Add(append(append([]byte{}, legit.PEM...), forged.PEM...), auditInfo(legit))
	f.Add(root.PEM, []byte(`{"EID":"root","RH":null}`))

	f.Fuzz(func(t *testing.T, id, raw []byte) {
		require.NotPanics(t, func() {
			ai, err := des.DeserializeAuditInfo(t.Context(), id, raw)
			if err == nil {
				cert, err := crypto.ParseOwnerCertificate(id)
				require.NoError(t, err)
				require.NoError(t, checker.cas.VerifyOwner(cert))
				require.Equal(t, crypto.EnrollmentIDFromCert(cert), ai.EnrollmentID())
			}
			m, err := matchers.GetAuditInfoMatcher(t.Context(), nil, raw)
			if err == nil {
				_ = m.Match(t.Context(), id)
			}
		})
	})
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/stretchr/testify/require"
)

const maxFuzzAuditInfoBytes = 64 << 10

// fuzzMember returns a typed x509 member identity and its audit info bytes.
func fuzzMember(f *testing.F, name, eid string) ([]byte, []byte) {
	f.Helper()
	member, err := identity.WrapWithType(x509.IdentityType, []byte(name))
	require.NoError(f, err)
	auditInfo, err := (&x509.AuditInfo{EID: eid, RH: []byte("rh-" + eid)}).Bytes()
	require.NoError(f, err)

	return member, auditInfo
}

// fuzzPolicy wraps members into a policy identity (inner DER and typed
// envelope) and the matching composite audit info blob.
func fuzzPolicy(f *testing.F, policy string, members, auditInfos [][]byte) (inner, envelope, wrapped []byte) {
	f.Helper()
	inner, err := (&PolicyIdentity{Policy: policy, Identities: members}).Serialize()
	require.NoError(f, err)
	envelope, err = identity.WrapWithType(Policy, inner)
	require.NoError(f, err)
	wrapped, err = WrapAuditInfo(auditInfos)
	require.NoError(f, err)

	return inner, envelope, wrapped
}

func FuzzDeserializeAuditInfoNoPanic(f *testing.F) {
	m0, ai0 := fuzzMember(f, "cert-zero", "wallet-42")
	m1, ai1 := fuzzMember(f, "cert-one", "wallet-43")

	// valid single- and two-member policies, both identity encodings
	inner1, envelope1, wrapped1 := fuzzPolicy(f, "$0", [][]byte{m0}, [][]byte{ai0})
	f.Add(inner1, wrapped1)
	f.Add(envelope1, wrapped1)
	inner2, _, wrapped2 := fuzzPolicy(f, "$0 OR $1", [][]byte{m0, m1}, [][]byte{ai0, ai1})
	f.Add(inner2, wrapped2)
	// empty and truncated inputs
	f.Add([]byte{}, []byte{})
	f.Add(inner2[:len(inner2)/2], wrapped2[:len(wrapped2)/2])
	f.Add(inner2, []byte(`{"IdentityAuditInfos":`))
	f.Add(inner1, []byte(`{}`))
	// member count mismatch
	innerMismatch, _, wrappedOne := fuzzPolicy(f, "$0 OR $1", [][]byte{m0, m1}, [][]byte{ai0})
	f.Add(innerMismatch, wrappedOne)
	// unknown member identity type
	unknown, err := identity.WrapWithType(identity.Type(99), []byte("cert-x"))
	require.NoError(f, err)
	innerUnknown, _, wrappedUnknown := fuzzPolicy(f, "$0", [][]byte{unknown}, [][]byte{ai0})
	f.Add(innerUnknown, wrappedUnknown)
	// nested and deeply nested policies
	nestedID, nestedInfo := m0, ai0
	for range 5 {
		_, nestedID, nestedInfo = fuzzPolicy(f, "$0", [][]byte{nestedID}, [][]byte{nestedInfo})
	}
	innerDeep, _, wrappedDeep := fuzzPolicy(f, "$0", [][]byte{nestedID}, [][]byte{nestedInfo})
	f.Add(innerDeep, wrappedDeep)

	f.Fuzz(func(t *testing.T, rawID, rawInfo []byte) {
		if len(rawID) > maxFuzzAuditInfoBytes || len(rawInfo) > maxFuzzAuditInfoBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = NewAuditInfoDeserializer(newEIDRHDeserializer()).DeserializeAuditInfo(t.Context(), rawID, rawInfo)
		})
	})
}

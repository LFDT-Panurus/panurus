/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/stretchr/testify/require"
)

const maxFuzzAuditInfoBytes = 64 << 10

// fuzzMultisig wraps members into a multisig identity (inner DER and typed
// envelope) and the matching multisig audit info blob.
func fuzzMultisig(f *testing.F, members, auditInfos [][]byte) (inner, envelope, wrapped []byte) {
	f.Helper()
	ids := make([]token.Identity, len(members))
	for i, m := range members {
		ids[i] = m
	}
	inner, err := (&MultiIdentity{Identities: ids}).Bytes()
	require.NoError(f, err)
	envelope, err = identity.WrapWithType(Multisig, inner)
	require.NoError(f, err)
	wrapped, err = WrapAuditInfo(auditInfos)
	require.NoError(f, err)

	return inner, envelope, wrapped
}

// FuzzAuditInfoDeserializer_DeserializeAuditInfo hunts for an identity and
// audit info pair that panics AuditInfoDeserializer.DeserializeAuditInfo, which
// decodes request-carried audit info and recurses into the members.
func FuzzAuditInfoDeserializer_DeserializeAuditInfo(f *testing.F) {
	m0, err := identity.WrapWithType(x509.IdentityType, []byte("cert-zero"))
	require.NoError(f, err)
	ai0, err := (&x509.AuditInfo{EID: "alice", RH: []byte("rh-alice")}).Bytes()
	require.NoError(f, err)
	m1, err := identity.WrapWithType(x509.IdentityType, []byte("cert-one"))
	require.NoError(f, err)
	ai1, err := (&x509.AuditInfo{EID: "alice", RH: []byte("rh-alice")}).Bytes()
	require.NoError(f, err)

	// valid, both identity encodings
	inner, envelope, wrapped := fuzzMultisig(f, [][]byte{m0, m1}, [][]byte{ai0, ai1})
	f.Add(inner, wrapped)
	f.Add(envelope, wrapped)
	// empty and truncated inputs
	f.Add([]byte{}, []byte{})
	f.Add(inner[:len(inner)/2], wrapped[:len(wrapped)/2])
	f.Add(inner, []byte(`{"IdentityAuditInfos":`))
	f.Add(inner, []byte(`{}`))
	// member count mismatch
	_, _, wrappedOne := fuzzMultisig(f, [][]byte{m0}, [][]byte{ai0})
	f.Add(inner, wrappedOne)
	// nested multisig
	nestedID, nestedInfo := envelope, wrapped
	for range 5 {
		_, nestedID, nestedInfo = fuzzMultisig(f, [][]byte{nestedID}, [][]byte{nestedInfo})
	}
	innerDeep, _, wrappedDeep := fuzzMultisig(f, [][]byte{nestedID}, [][]byte{nestedInfo})
	f.Add(innerDeep, wrappedDeep)

	f.Fuzz(func(t *testing.T, rawID, rawInfo []byte) {
		if len(rawID) > maxFuzzAuditInfoBytes || len(rawInfo) > maxFuzzAuditInfoBytes {
			t.Skip()
		}
		d := deserializer.NewEIDRHDeserializer()
		d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{})
		d.AddDeserializer(Multisig, NewAuditInfoDeserializer(d))
		require.NotPanics(t, func() {
			_, _ = NewAuditInfoDeserializer(d).DeserializeAuditInfo(t.Context(), rawID, rawInfo)
		})
	})
}

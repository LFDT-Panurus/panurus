/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzMultisigBytes = 64 << 10

// FuzzMultiIdentityDeserializeNoPanic hunts for malformed ASN.1 that panics
// MultiIdentity.Deserialize instead of returning an error. This is the
// deserialization entry point for multisig identities across
// DeserializeVerifier, GetAuditInfoMatcher, Recipients and Unwrap.
func FuzzMultiIdentityDeserializeNoPanic(f *testing.F) {
	valid, err := (&MultiIdentity{Identities: []token.Identity{
		[]byte("alice"), []byte("bob"),
	}}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte("not asn1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzMultisigBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			mid := &MultiIdentity{}
			_ = mid.Deserialize(raw)
		})
	})
}

// FuzzMultiSignatureFromBytesNoPanic hunts for malformed ASN.1 that panics
// MultiSignature.FromBytes instead of returning an error. This is the
// deserialization entry point invoked directly on peer-supplied signature
// bytes in Verifier.Verify.
func FuzzMultiSignatureFromBytesNoPanic(f *testing.F) {
	valid, err := (&MultiSignature{Signatures: [][]byte{[]byte("sig1"), []byte("sig2")}}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte("not asn1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzMultisigBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			sig := &MultiSignature{}
			_ = sig.FromBytes(raw)
		})
	})
}

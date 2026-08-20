/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"encoding/asn1"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzMultisigBytes = 64 << 10

// FuzzMultiIdentityDeserializeNoPanic hunts for malformed ASN.1 that panics
// MultiIdentity.Deserialize instead of returning an error. This is the
// deserialization entry point for multisig identities across
// DeserializeVerifier, GetAuditInfoMatcher, Recipients and Unwrap.
// It also pins the canonicality contract at this call site: any input
// Deserialize accepts must re-serialize to exactly those bytes, because
// Identity.UniqueID() hashes the raw bytes and a second accepted spelling of one
// identity is a second cache slot for it across the identity and wallet layers.
//
// Note what this does and does not buy. UnmarshalCanonicalDER currently enforces the
// contract by re-marshalling and comparing, which is the same computation
// Bytes() performs — so against today's implementation the assertion cannot
// fail, and it is not what found the bug it was written for. Its value is as a
// regression guard: it is stated in terms of the contract rather than the
// implementation, so it fails if UnmarshalCanonicalDER is ever swapped for a cheaper
// hand-rolled check that misses a case. Coverage of the actual vectors lives in
// security_test.go, which asserts against fixed attacker-crafted bytes.
func FuzzMultiIdentityDeserializeNoPanic(f *testing.F) {
	ids := []token.Identity{[]byte("alice"), []byte("bob")}
	valid, err := (&MultiIdentity{Identities: ids}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	// Garbage appended after the top-level value...
	f.Add(append(append([]byte{}, valid...), 0xDE, 0xAD))
	// ...and the same garbage smuggled inside the SEQUENCE, which a check on
	// asn1.Unmarshal's "rest" cannot see.
	smuggled, err := asn1.Marshal(multiIdentityWithExtra{Identities: ids, Extra: 42})
	require.NoError(f, err)
	f.Add(smuggled)
	f.Add([]byte{})
	f.Add([]byte("not asn1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzMultisigBytes {
			t.Skip()
		}
		mid := &MultiIdentity{}
		var err error
		require.NotPanics(t, func() {
			err = mid.Deserialize(raw)
		})
		if err != nil {
			return
		}
		reencoded, err := mid.Bytes()
		require.NoError(t, err, "an accepted MultiIdentity must be re-serializable")
		require.Equal(t, raw, reencoded,
			"accepted bytes must be the canonical encoding of the decoded identity")
	})
}

// FuzzMultiSignatureFromBytesNoPanic hunts for malformed ASN.1 that panics
// MultiSignature.FromBytes instead of returning an error. This is the
// deserialization entry point invoked directly on peer-supplied signature
// bytes in Verifier.Verify.
func FuzzMultiSignatureFromBytesNoPanic(f *testing.F) {
	sigs := [][]byte{[]byte("sig1"), []byte("sig2")}
	valid, err := (&MultiSignature{Signatures: sigs}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	f.Add(append(append([]byte{}, valid...), 0x00))
	smuggled, err := asn1.Marshal(multiSignatureWithExtra{Signatures: sigs, Extra: 7})
	require.NoError(f, err)
	f.Add(smuggled)
	f.Add([]byte{})
	f.Add([]byte("not asn1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzMultisigBytes {
			t.Skip()
		}
		sig := &MultiSignature{}
		var err error
		require.NotPanics(t, func() {
			err = sig.FromBytes(raw)
		})
		if err != nil {
			return
		}
		reencoded, err := sig.Bytes()
		require.NoError(t, err, "an accepted MultiSignature must be re-serializable")
		// Contract-level regression guard; see FuzzMultiIdentityDeserializeNoPanic
		// for what this does and does not buy.
		require.Equal(t, raw, reencoded,
			"accepted bytes must be the canonical encoding of the decoded signature")
	})
}

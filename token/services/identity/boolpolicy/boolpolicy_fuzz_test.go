/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"encoding/asn1"
	"testing"

	"github.com/stretchr/testify/require"
)

const maxFuzzBoolPolicyBytes = 64 << 10

// FuzzPolicyIdentityDeserializeNoPanic hunts for malformed ASN.1 that panics
// PolicyIdentity.Deserialize instead of returning an error. This is the
// deserialization entry point for policy identities across DeserializeVerifier,
// GetAuditInfoMatcher, Recipients and Unwrap, and it mirrors
// FuzzMultiIdentityDeserializeNoPanic in the structurally identical multisig
// package.
func FuzzPolicyIdentityDeserializeNoPanic(f *testing.F) {
	valid, err := (&PolicyIdentity{
		Policy:     "$0 OR ($1 AND $2)",
		Identities: [][]byte{[]byte("alice"), []byte("bob"), []byte("carol")},
	}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	// Trailing bytes after the canonical encoding — the laxness this decode
	// path was hardened against; it must now be a clean error, never a panic.
	f.Add(append(append([]byte{}, valid...), 0xDE, 0xAD))
	// An element smuggled *inside* the SEQUENCE rather than appended after it.
	// A rest check cannot see this one, so it is the shape most likely to hide
	// a regression.
	smuggled, err := asn1.Marshal(policyIdentityWithExtra{
		Policy:     "$0 OR ($1 AND $2)",
		Identities: [][]byte{[]byte("alice"), []byte("bob"), []byte("carol")},
		Extra:      42,
	})
	require.NoError(f, err)
	f.Add(smuggled)
	f.Add([]byte{})
	f.Add([]byte("not asn1"))
	f.Add([]byte{0x30, 0x81})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBoolPolicyBytes {
			t.Skip()
		}
		pi := &PolicyIdentity{}
		var err error
		require.NotPanics(t, func() {
			err = pi.Deserialize(raw)
		})
		if err != nil {
			return
		}
		// Canonicality contract: raw was accepted, so it must be the one
		// encoding of what it decoded to — any other accepted spelling is a
		// second Identity.UniqueID() cache slot for one identity. This is a
		// regression guard stated in terms of the contract, not an independent
		// check: UnmarshalCanonicalDER enforces it by re-marshalling and comparing,
		// which is what Bytes() does, so it cannot fail against the current
		// implementation. security_test.go carries the actual vectors.
		reencoded, err := pi.Bytes()
		require.NoError(t, err, "an accepted PolicyIdentity must be re-serializable")
		require.Equal(t, raw, reencoded,
			"accepted bytes must be the canonical encoding of the decoded identity")
	})
}

// FuzzPolicySignatureFromBytesNoPanic hunts for malformed ASN.1 that panics
// PolicySignature.FromBytes instead of returning an error. This is the
// deserialization entry point invoked directly on peer-supplied signature bytes
// in PolicyVerifier.Verify.
func FuzzPolicySignatureFromBytesNoPanic(f *testing.F) {
	sigs := [][]byte{[]byte("sig1"), nil, []byte("sig3")}
	valid, err := (&PolicySignature{Signatures: sigs}).Bytes()
	require.NoError(f, err)
	f.Add(valid)
	f.Add(append(append([]byte{}, valid...), 0x00))
	smuggled, err := asn1.Marshal(policySignatureWithExtra{Signatures: sigs, Extra: 7})
	require.NoError(f, err)
	f.Add(smuggled)
	f.Add([]byte{})
	f.Add([]byte("not asn1"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzBoolPolicyBytes {
			t.Skip()
		}
		sig := &PolicySignature{}
		var err error
		require.NotPanics(t, func() {
			err = sig.FromBytes(raw)
		})
		if err != nil {
			return
		}
		reencoded, err := sig.Bytes()
		require.NoError(t, err, "an accepted PolicySignature must be re-serializable")
		// Contract-level regression guard; see FuzzPolicyIdentityDeserializeNoPanic.
		require.Equal(t, raw, reencoded,
			"accepted bytes must be the canonical encoding of the decoded signature")
	})
}

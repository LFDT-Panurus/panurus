/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test multi-signature related operations

// Test serializing and deserialing a MultiSignature
func TestMultiSignature_FromBytes(t *testing.T) {
	original := &MultiSignature{
		Signatures: [][]byte{
			[]byte("sig1"),
			[]byte("sig2"),
		},
	}

	bytes, err := original.Bytes()
	require.NoError(t, err)

	decoded := &MultiSignature{}
	err = decoded.FromBytes(bytes)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

// Test failure to deserialize an invalid raw MultiSignature
func TestMultiSignature_FromBytes_Invalid(t *testing.T) {
	sig := &MultiSignature{}
	err := sig.FromBytes([]byte("invalid"))
	require.Error(t, err)
}

// Test deserializing a multi-sig from a raw joining of multi-ids and multi-sigs
func TestJoinSignatures_Success(t *testing.T) {
	id1 := token.Identity([]byte("id1"))
	id2 := token.Identity([]byte("id2"))

	identities := []token.Identity{id1, id2}

	sigmas := map[string][]byte{
		id1.UniqueID(): []byte("sig1"),
		id2.UniqueID(): []byte("sig2"),
	}

	joined, err := JoinSignatures(identities, sigmas)
	require.NoError(t, err)
	assert.NotEmpty(t, joined)

	// Verify the joined signature can be decoded
	sig := &MultiSignature{}
	err = sig.FromBytes(joined)
	require.NoError(t, err)
	assert.Len(t, sig.Signatures, 2)
	assert.Equal(t, []byte("sig1"), sig.Signatures[0])
	assert.Equal(t, []byte("sig2"), sig.Signatures[1])
}

// Test failure to join a multi-ids with two ids and a multi-sigs with just one sig

func TestJoinSignatures_MissingSignature(t *testing.T) {
	identities := []token.Identity{
		[]byte("id1"),
		[]byte("id2"),
	}

	sigmas := map[string][]byte{
		string([]byte("id1")): []byte("sig1"),
		// Missing signature for id2
	}

	joined, err := JoinSignatures(identities, sigmas)
	require.Error(t, err)
	assert.Nil(t, joined)
	assert.Contains(t, err.Error(), "signature for identity")
	assert.Contains(t, err.Error(), "is missing")
}

// Create a raw multi-sig of two sigs and use it to verify a sig against a message
func TestVerifier_Verify_Success(t *testing.T) {
	msg := []byte("test message")
	sig1 := []byte("signature1")
	sig2 := []byte("signature2")

	verifier1 := &mock.Verifier{}
	verifier1.VerifyReturns(nil)
	verifier2 := &mock.Verifier{}
	verifier2.VerifyReturns(nil)

	verifier := &Verifier{
		Verifiers: []driver.Verifier{verifier1, verifier2},
	}

	multiSig := &MultiSignature{
		Signatures: [][]byte{sig1, sig2},
	}
	multiSigBytes, err := multiSig.Bytes()
	require.NoError(t, err)

	err = verifier.Verify(msg, multiSigBytes)
	require.NoError(t, err)

	// Verify each verifier was called once
	assert.Equal(t, 1, verifier1.VerifyCallCount())
	assert.Equal(t, 1, verifier2.VerifyCallCount())
}

// Create a raw multi-sig of two sigs, one of which is invalid, and then fail
// to verify the multi-sig against a message use it to verify a sig against a message
// because the corresponding verifier fails
func TestVerifier_Verify_InvalidSignature(t *testing.T) {
	msg := []byte("test message")
	sig1 := []byte("signature1")
	sig2 := []byte("wrong_signature")

	verifier1 := &mock.Verifier{}
	verifier1.VerifyReturns(nil)
	verifier2 := &mock.Verifier{}
	verifier2.VerifyReturns(assert.AnError)

	verifier := &Verifier{
		Verifiers: []driver.Verifier{verifier1, verifier2},
	}

	multiSig := &MultiSignature{
		Signatures: [][]byte{sig1, sig2},
	}
	multiSigBytes, err := multiSig.Bytes()
	require.NoError(t, err)

	err = verifier.Verify(msg, multiSigBytes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature at index")
	assert.Contains(t, err.Error(), "does not verify")

	// Verify first verifier was called, second verifier was called and returned error
	assert.Equal(t, 1, verifier1.VerifyCallCount())
	assert.Equal(t, 1, verifier2.VerifyCallCount())
}

// Test failure to verify a message with an invalid multi-signature
func TestVerifier_Verify_InvalidMultisigBytes(t *testing.T) {
	verifier1 := &mock.Verifier{}

	verifier := &Verifier{
		Verifiers: []driver.Verifier{verifier1},
	}

	err := verifier.Verify([]byte("msg"), []byte("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal multisig")

	// Verify no verifiers were called (failed during unmarshal)
	assert.Equal(t, 0, verifier1.VerifyCallCount())
}

// Verify returns an error immediately when the Verifier has no sub-verifiers,
// even if the raw signature encodes an empty MultiSignature{} (the "anyone-can-spend"
// exploit path patched in sig.go).
func TestVerifier_Verify_ZeroVerifiers(t *testing.T) {
	emptyMultiSig := &MultiSignature{Signatures: [][]byte{}}
	emptyBytes, err := emptyMultiSig.Bytes()
	require.NoError(t, err)

	verifier := &Verifier{Verifiers: []driver.Verifier{}}

	err = verifier.Verify([]byte("any message"), emptyBytes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multisig verifier has no members")
}

// A MultiIdentity whose Identities slice repeats the SAME member (e.g. three
// slots all bound to Alice, meant to look like a 3-of-3 policy) lets one real
// signature from Alice satisfy every slot: JoinSignatures only needs one map
// entry per unique UniqueID, and Verifier.Verify has no logic detecting or
// rejecting duplicate members. A "3 signers required" policy is silently
// equivalent to "1 signer, repeated" whenever the identity list contains
// duplicates.
func TestVerifier_Verify_DuplicateMemberSingleSignerBypass(t *testing.T) {
	alice := token.Identity([]byte("alice-pubkey"))
	msg := []byte("test message")

	mid := &MultiIdentity{Identities: []token.Identity{alice, alice, alice}}

	verifierForAlice := &mock.Verifier{}
	verifierForAlice.VerifyReturns(nil) // any of the 3 slots verifies with Alice's one real key

	verifier := &Verifier{
		Verifiers: []driver.Verifier{verifierForAlice, verifierForAlice, verifierForAlice},
	}

	// Alice supplies exactly one real signature, keyed by her own UniqueID.
	sigmas := map[string][]byte{alice.UniqueID(): []byte("alice-real-sig")}
	joined, err := JoinSignatures(mid.Identities, sigmas)
	require.NoError(t, err)

	err = verifier.Verify(msg, joined)
	require.NoError(t, err, "a single real signature from a duplicated member satisfies all of its slots")
	assert.Equal(t, 3, verifierForAlice.VerifyCallCount())
}

// Create a raw multi-sig of one sig  and fail to verify it with a multi-verifier
// with two verifiers
func TestVerifier_Verify_SignatureCountMismatch(t *testing.T) {
	msg := []byte("test message")

	verifier1 := &mock.Verifier{}
	verifier2 := &mock.Verifier{}

	verifier := &Verifier{
		Verifiers: []driver.Verifier{verifier1, verifier2},
	}

	// Only one signature, but two verifiers
	multiSig := &MultiSignature{
		Signatures: [][]byte{[]byte("sig1")},
	}
	multiSigBytes, err := multiSig.Bytes()
	require.NoError(t, err)

	err = verifier.Verify(msg, multiSigBytes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid multisig: expect")
	assert.Contains(t, err.Error(), "signatures, but received")

	// Verify no verifiers were called (count mismatch detected before verification)
	assert.Equal(t, 0, verifier1.VerifyCallCount())
	assert.Equal(t, 0, verifier2.VerifyCallCount())
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
	"encoding/asn1"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/stretchr/testify/require"
)

// TestNoneComponentIdentityRejectedAtWrapTime proves that WrapIdentities now
// rejects a "none" (empty) component identity outright instead of silently
// accepting it.
//
// Previously, WrapIdentities only rejected len(ids) == 0; it never rejected
// an individual empty component identity. Downstream,
// TypedVerifierDeserializerMultiplex.GetAuditInfoMatcher returned (nil, nil)
// — no error — whenever the component identity's IsNone() is true, and
// neither TypedIdentityDeserializer.GetAuditInfoMatcher (deserializer.go)
// nor InfoMatcher.Match (identity.go) checked the resulting matcher slot for
// nil before calling Match on it, so a multisig identity carrying a "none"
// component identity nil-pointer-dereferenced at match time — the same gap
// independently confirmed for boolpolicy's structurally identical
// deserializer. Rejecting the empty identity at Wrap-time closes the gap
// for honest callers; TestNoneComponentIdentityRejectedAtDeserializeTime
// below closes it for the real attack surface (raw wire bytes).
func TestNoneComponentIdentityRejectedAtWrapTime(t *testing.T) {
	_, err := WrapIdentities(token.Identity{})
	require.Error(t, err, "an empty/none component identity must be rejected at Wrap time")
}

// TestNoneComponentIdentityRejectedAtDeserializeTime proves that the same
// none-identity check also guards the real attack surface: an attacker who
// crafts a MultiIdentity's raw DER bytes directly (rather than calling
// WrapIdentities) is still caught, this time by
// TypedIdentityDeserializer.GetAuditInfoMatcher, the entry point that
// previously produced a nil Matcher slot and nil-pointer-dereferenced at
// match time.
func TestNoneComponentIdentityRejectedAtDeserializeTime(t *testing.T) {
	ctx := context.Background()

	mi := &MultiIdentity{Identities: []token.Identity{{}}}
	inner, err := mi.Bytes()
	require.NoError(t, err)

	envelope, err := (&identity.TypedIdentity{Type: Multisig, Identity: inner}).Bytes()
	require.NoError(t, err)

	// Mirrors real production wiring, e.g.
	// token/core/fabtoken/v1/driver/deserializer.go, where the same
	// multiplex deserializer is passed as both the VerifierDES and the
	// AuditInfoMatcher to multisig.NewTypedIdentityDeserializer.
	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	d := NewTypedIdentityDeserializer(des, des)

	auditInfoBytes, err := (&AuditInfo{IdentityAuditInfos: []IdentityAuditInfo{{AuditInfo: nil}}}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfoMatcher(ctx, envelope, auditInfoBytes)
	require.Error(t, err, "a multisig identity with a none component identity must be rejected at deserialize time")
}

// TestDuplicateIdentityRejectedAtWrapTime proves that WrapIdentities now
// rejects a duplicated component identity outright — the multisig-native
// analog of boolpolicy's AND-policy duplicate-identity bypass. Previously,
// JoinSignatures keyed signatures purely by identity.UniqueID() and
// Verifier.Verify had no duplicate-member detection, so a MultiIdentity that
// repeated the same identity across multiple "signer" slots (e.g.
// simulating a 3-of-3 policy where all 3 slots are actually one person) was
// satisfied by that one identity's single real signature.
func TestDuplicateIdentityRejectedAtWrapTime(t *testing.T) {
	id0 := token.Identity("identity-zero")
	_, err := WrapIdentities(id0, id0)
	require.Error(t, err, "a duplicated component identity must be rejected at Wrap time")
}

// TestDuplicateIdentityRejectedAtDeserializeTime proves that the same
// duplicate-identity check also guards the real attack surface: an attacker
// who crafts a MultiIdentity's raw DER bytes directly (rather than calling
// WrapIdentities) is still caught, this time by
// TypedIdentityDeserializer.DeserializeVerifier, the entry point the
// verification path actually uses to build the Verifier from
// attacker-supplied wire bytes.
func TestDuplicateIdentityRejectedAtDeserializeTime(t *testing.T) {
	ctx := context.Background()

	id0 := token.Identity("identity-zero")
	mi := &MultiIdentity{Identities: []token.Identity{id0, id0}}
	raw, err := mi.Bytes()
	require.NoError(t, err)

	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	d := NewTypedIdentityDeserializer(des, des)

	_, err = d.DeserializeVerifier(ctx, Multisig, raw)
	require.Error(t, err, "a multisig identity with a duplicated component identity must be rejected at deserialize time")
}

// TestTrailingBytesRejectedOnIdentityDeserialize is the reproduction from the
// issue: MultiIdentity.Deserialize discarded encoding/asn1.Unmarshal's "rest"
// return, so a canonical MultiIdentity with garbage appended still deserialized
// successfully — while hashing to a *different* Identity.UniqueID(), because
// UniqueID() hashes the raw bytes rather than a canonicalised form of the
// decoded value. Since UniqueID() is the cache key throughout the
// identity/wallet layers (role/registry.go's fast path, provider.go's signer
// cache, …), one logical identity could occupy two cache slots.
func TestTrailingBytesRejectedOnIdentityDeserialize(t *testing.T) {
	canonical, err := (&MultiIdentity{Identities: []token.Identity{
		token.Identity("alice"), token.Identity("bob"),
	}}).Bytes()
	require.NoError(t, err)

	require.NoError(t, (&MultiIdentity{}).Deserialize(canonical),
		"the canonical encoding must still deserialize")

	padded := append(append([]byte{}, canonical...), 0xDE, 0xAD, 0xBE, 0xEF)

	require.Error(t, (&MultiIdentity{}).Deserialize(padded),
		"a MultiIdentity with trailing bytes appended must be rejected")

	// This inequality is why the lenient parse mattered: the two byte strings
	// decoded to the same logical identity but keyed the caches differently.
	require.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(padded).UniqueID(),
		"UniqueID() hashes the raw bytes, so the padded form was a distinct cache key")
}

// TestTrailingBytesRejectedOnSignatureFromBytes covers the same laxness in
// MultiSignature.FromBytes, which is invoked directly on peer-supplied bytes in
// Verifier.Verify. Only the multisig *envelope* is tightened here; the
// individual signatures it carries are still parsed by their own verifiers,
// which must stay lenient for external signers and HSMs.
func TestTrailingBytesRejectedOnSignatureFromBytes(t *testing.T) {
	canonical, err := (&MultiSignature{Signatures: [][]byte{
		[]byte("sig1"), []byte("sig2"),
	}}).Bytes()
	require.NoError(t, err)

	require.NoError(t, (&MultiSignature{}).FromBytes(canonical),
		"the canonical encoding must still deserialize")

	padded := append(append([]byte{}, canonical...), 0x00)

	require.Error(t, (&MultiSignature{}).FromBytes(padded),
		"a MultiSignature with trailing bytes appended must be rejected")
}

// multiIdentityWithExtra is MultiIdentity plus one undeclared trailing element.
// Marshalling it is how an attacker writes the smuggled form: encoding/asn1
// consumes only as many SEQUENCE elements as the destination struct has fields
// and silently drops the rest, so these bytes decode into a plain MultiIdentity
// with nothing left over.
type multiIdentityWithExtra struct {
	Identities []token.Identity
	Extra      int32
}

// TestExtraElementInsideSequenceRejectedOnIdentityDeserialize covers the vector
// the trailing-byte check alone cannot see. Appending garbage *after* the
// top-level TLV is caught by asn1.Unmarshal's "rest"; moving that same garbage
// *inside* the SEQUENCE, with the outer length grown to cover it, leaves rest
// empty — so the identity decodes to {alice, bob} under raw bytes that hash to
// a different UniqueID(). Concretely: a token transferred to the smuggled form
// verifies (the verifier works on the decoded identities) but never resolves to
// alice's or bob's wallet (the lookup works on UniqueID()), leaving a valid
// token neither owner can see.
func TestExtraElementInsideSequenceRejectedOnIdentityDeserialize(t *testing.T) {
	ids := []token.Identity{token.Identity("alice"), token.Identity("bob")}

	canonical, err := (&MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)
	require.NoError(t, (&MultiIdentity{}).Deserialize(canonical),
		"the canonical encoding must still deserialize")

	smuggled, err := asn1.Marshal(multiIdentityWithExtra{Identities: ids, Extra: 42})
	require.NoError(t, err)
	require.NotEqual(t, canonical, smuggled, "the two encodings must really differ")

	// Baseline: the lax decode accepts it, yields the same identities, and has
	// nothing left over for a rest check to catch.
	var lenient MultiIdentity
	rest, err := asn1.Unmarshal(smuggled, &lenient)
	require.NoError(t, err)
	require.Empty(t, rest, "the extra element is inside the SEQUENCE, so there is no tail to reject")
	require.Equal(t, ids, lenient.Identities, "same logical identity, different bytes")

	require.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(smuggled).UniqueID(),
		"UniqueID() hashes the raw bytes, so the smuggled form is a distinct cache key")

	require.Error(t, (&MultiIdentity{}).Deserialize(smuggled),
		"a MultiIdentity with an element smuggled inside the SEQUENCE must be rejected")
}

// multiSignatureWithExtra is MultiSignature plus one undeclared element, the
// signature-envelope analog of multiIdentityWithExtra.
type multiSignatureWithExtra struct {
	Signatures [][]byte
	Extra      int32
}

// TestExtraElementInsideSequenceRejectedOnSignatureFromBytes covers the same
// vector in MultiSignature.FromBytes, which Verifier.Verify calls directly on
// peer-supplied bytes.
func TestExtraElementInsideSequenceRejectedOnSignatureFromBytes(t *testing.T) {
	sigs := [][]byte{[]byte("sig1"), []byte("sig2")}

	canonical, err := (&MultiSignature{Signatures: sigs}).Bytes()
	require.NoError(t, err)
	require.NoError(t, (&MultiSignature{}).FromBytes(canonical),
		"the canonical encoding must still deserialize")

	smuggled, err := asn1.Marshal(multiSignatureWithExtra{Signatures: sigs, Extra: 7})
	require.NoError(t, err)

	require.Error(t, (&MultiSignature{}).FromBytes(smuggled),
		"a MultiSignature with an element smuggled inside the SEQUENCE must be rejected")
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"context"
	"encoding/asn1"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/stretchr/testify/require"
)

// TestNoneComponentIdentityRejectedAtWrapTime proves that WrapPolicyIdentity
// now rejects a "none" (empty) component identity outright instead of
// silently accepting it.
//
// Previously, WrapPolicyIdentity only rejected len(ids) == 0 and
// policy == ""; it never rejected an individual empty component identity.
// Downstream, TypedVerifierDeserializerMultiplex.GetAuditInfoMatcher
// returns (nil, nil) — no error — whenever the component identity's
// IsNone() is true, and neither TypedIdentityDeserializer.GetAuditInfoMatcher
// (deserializer.go) nor InfoMatcher.Match (identity.go) checked the
// resulting matcher slot for nil before calling Match on it, so a policy
// identity carrying a "none" component identity nil-pointer-dereferenced at
// match time. Rejecting the empty identity at Wrap-time closes the gap for
// honest callers; TestNoneComponentIdentityRejectedAtDeserializeTime below
// closes it for the real attack surface (raw wire bytes).
func TestNoneComponentIdentityRejectedAtWrapTime(t *testing.T) {
	_, err := WrapPolicyIdentity("$0", token.Identity{})
	require.Error(t, err, "an empty/none component identity must be rejected at Wrap time")
}

// TestNoneComponentIdentityRejectedAtDeserializeTime proves that the same
// none-identity check also guards the real attack surface: an attacker who
// crafts a PolicyIdentity's raw DER bytes directly (rather than calling
// WrapPolicyIdentity) is still caught, this time by
// TypedIdentityDeserializer.GetAuditInfoMatcher, the entry point that
// previously produced a nil Matcher slot and nil-pointer-dereferenced at
// match time.
func TestNoneComponentIdentityRejectedAtDeserializeTime(t *testing.T) {
	ctx := context.Background()

	pi := &PolicyIdentity{Policy: "$0", Identities: [][]byte{token.Identity{}}}
	inner, err := pi.Bytes()
	require.NoError(t, err)

	envelope, err := (&identity.TypedIdentity{Type: Policy, Identity: inner}).Bytes()
	require.NoError(t, err)

	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	d := NewTypedIdentityDeserializer(des, des)

	auditInfoBytes, err := (&AuditInfo{IdentityAuditInfos: []IdentityAuditInfo{{AuditInfo: nil}}}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfoMatcher(ctx, envelope, auditInfoBytes)
	require.Error(t, err, "a policy identity with a none component identity must be rejected at deserialize time")
}

// TestDuplicateIdentityRejectedAtWrapTime proves that WrapPolicyIdentity now
// rejects a duplicated component identity outright.
//
// Previously, WrapPolicyIdentity never checked that its component
// identities were distinct. JoinSignatures keys signatures purely by
// id.UniqueID(), so a duplicated identity across multiple $N slots received
// the very same signature bytes in every slot where it appeared, and
// PolicyVerifier.evalNode's AndNode case independently re-verified each
// RefNode slot against its own Verifiers[i] — so "$0 AND $1" with
// Identities[0] == Identities[1] was satisfied by one real signer's single
// signature counted twice.
func TestDuplicateIdentityRejectedAtWrapTime(t *testing.T) {
	_, err := WrapPolicyIdentity("$0 AND $1", id0, id0)
	require.Error(t, err, "a duplicated component identity must be rejected at Wrap time")
}

// TestDuplicateIdentityRejectedAtDeserializeTime proves that the same
// duplicate-identity check also guards the real attack surface: an attacker
// who crafts a PolicyIdentity's raw DER bytes directly (rather than calling
// WrapPolicyIdentity) is still caught, this time by
// TypedIdentityDeserializer.DeserializeVerifier, the entry point the
// verification path actually uses to build the PolicyVerifier from
// attacker-supplied wire bytes.
func TestDuplicateIdentityRejectedAtDeserializeTime(t *testing.T) {
	ctx := context.Background()

	pi := &PolicyIdentity{Policy: "$0 AND $1", Identities: [][]byte{id0, id0}}
	raw, err := pi.Bytes()
	require.NoError(t, err)

	des := deserializer.NewTypedVerifierDeserializerMultiplex()
	d := NewTypedIdentityDeserializer(des, des)

	_, err = d.DeserializeVerifier(ctx, Policy, raw)
	require.Error(t, err, "a policy identity with a duplicated component identity must be rejected at deserialize time")
}

// TestTrailingBytesRejectedOnIdentityDeserialize is the reproduction from the
// issue: PolicyIdentity.Deserialize discarded encoding/asn1.Unmarshal's "rest"
// return, so a canonical PolicyIdentity with garbage appended still
// deserialized successfully — while hashing to a *different*
// Identity.UniqueID(), because UniqueID() hashes the raw bytes rather than a
// canonicalised form of the decoded value. Since UniqueID() is the cache key
// throughout the identity/wallet layers (role/registry.go's fast path,
// provider.go's signer cache, …), one logical identity could occupy two cache
// slots.
func TestTrailingBytesRejectedOnIdentityDeserialize(t *testing.T) {
	canonical, err := (&PolicyIdentity{Policy: "$0 OR $1", Identities: [][]byte{id0, id1}}).Bytes()
	require.NoError(t, err)

	require.NoError(t, (&PolicyIdentity{}).Deserialize(canonical),
		"the canonical encoding must still deserialize")

	padded := append(append([]byte{}, canonical...), 0xDE, 0xAD, 0xBE, 0xEF)

	require.Error(t, (&PolicyIdentity{}).Deserialize(padded),
		"a PolicyIdentity with trailing bytes appended must be rejected")

	// This inequality is why the lenient parse mattered: the two byte strings
	// decoded to the same logical identity but keyed the caches differently.
	require.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(padded).UniqueID(),
		"UniqueID() hashes the raw bytes, so the padded form was a distinct cache key")
}

// TestTrailingBytesRejectedOnSignatureFromBytes covers the same laxness in
// PolicySignature.FromBytes, which is invoked directly on peer-supplied bytes
// in PolicyVerifier.Verify. Only the policy *envelope* is tightened here; the
// individual signatures it carries are still parsed by their own verifiers,
// which must stay lenient for external signers and HSMs.
func TestTrailingBytesRejectedOnSignatureFromBytes(t *testing.T) {
	canonical, err := (&PolicySignature{Signatures: [][]byte{
		[]byte("sig1"), []byte("sig2"),
	}}).Bytes()
	require.NoError(t, err)

	require.NoError(t, (&PolicySignature{}).FromBytes(canonical),
		"the canonical encoding must still deserialize")

	padded := append(append([]byte{}, canonical...), 0x00)

	require.Error(t, (&PolicySignature{}).FromBytes(padded),
		"a PolicySignature with trailing bytes appended must be rejected")
}

// policyIdentityWithExtra is PolicyIdentity plus one undeclared trailing
// element. Marshalling it is how an attacker writes the smuggled form:
// encoding/asn1 consumes only as many SEQUENCE elements as the destination
// struct has fields and silently drops the rest, so these bytes decode into a
// plain PolicyIdentity with nothing left over.
type policyIdentityWithExtra struct {
	Policy     string `asn1:"utf8"`
	Identities [][]byte
	Extra      int32
}

// TestExtraElementInsideSequenceRejectedOnIdentityDeserialize covers the vector
// the trailing-byte check alone cannot see. Appending garbage *after* the
// top-level TLV is caught by asn1.Unmarshal's "rest"; moving that same garbage
// *inside* the SEQUENCE, with the outer length grown to cover it, leaves rest
// empty — so the identity decodes to the same policy over the same components
// under raw bytes that hash to a different UniqueID(). Concretely: a token
// transferred to the smuggled form verifies (the verifier works on the decoded
// policy) but never resolves to a component owner's wallet (the lookup works on
// UniqueID()), leaving a valid token none of the owners can see.
func TestExtraElementInsideSequenceRejectedOnIdentityDeserialize(t *testing.T) {
	const policy = "$0 OR $1"
	ids := [][]byte{id0, id1}

	canonical, err := (&PolicyIdentity{Policy: policy, Identities: ids}).Bytes()
	require.NoError(t, err)
	require.NoError(t, (&PolicyIdentity{}).Deserialize(canonical),
		"the canonical encoding must still deserialize")

	smuggled, err := asn1.Marshal(policyIdentityWithExtra{Policy: policy, Identities: ids, Extra: 42})
	require.NoError(t, err)
	require.NotEqual(t, canonical, smuggled, "the two encodings must really differ")

	// Baseline: the lax decode accepts it, yields the same policy identity, and
	// has nothing left over for a rest check to catch.
	var lenient PolicyIdentity
	rest, err := asn1.Unmarshal(smuggled, &lenient)
	require.NoError(t, err)
	require.Empty(t, rest, "the extra element is inside the SEQUENCE, so there is no tail to reject")
	require.Equal(t, policy, lenient.Policy)
	require.Equal(t, ids, lenient.Identities, "same logical identity, different bytes")

	require.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(smuggled).UniqueID(),
		"UniqueID() hashes the raw bytes, so the smuggled form is a distinct cache key")

	require.Error(t, (&PolicyIdentity{}).Deserialize(smuggled),
		"a PolicyIdentity with an element smuggled inside the SEQUENCE must be rejected")
}

// TestAlternateStringTagRejectedOnIdentityDeserialize covers a third spelling,
// unique to PolicyIdentity because it is the only one of the four envelopes
// carrying a string field: encoding/asn1 accepts T61String, IA5String and
// GeneralString wherever `asn1:"utf8"` was declared. Flipping that one tag byte
// leaves the decoded Policy identical and the raw bytes — hence the UniqueID()
// — different, with no trailing bytes and no length anomaly for either earlier
// check to catch.
func TestAlternateStringTagRejectedOnIdentityDeserialize(t *testing.T) {
	canonical, err := (&PolicyIdentity{Policy: "$0 OR $1", Identities: [][]byte{id0, id1}}).Bytes()
	require.NoError(t, err)

	// The Policy field is the first element of the SEQUENCE, so its tag is the
	// first byte of the body: [SEQUENCE, len, tag, ...].
	require.Equal(t, byte(asn1.TagUTF8String), canonical[2],
		"fixture assumption: canonical[2] is the Policy field's UTF8String tag")

	for _, tag := range []byte{
		byte(asn1.TagT61String),
		byte(asn1.TagIA5String),
		byte(asn1.TagGeneralString),
	} {
		retagged := append([]byte{}, canonical...)
		retagged[2] = tag

		// Baseline: same policy string, no leftovers.
		var lenient PolicyIdentity
		rest, err := asn1.Unmarshal(retagged, &lenient)
		require.NoError(t, err, "tag 0x%02X: encoding/asn1 accepts this where UTF8String was declared", tag)
		require.Empty(t, rest)
		require.Equal(t, "$0 OR $1", lenient.Policy, "tag 0x%02X: same policy, different bytes", tag)

		require.NotEqual(t, token.Identity(canonical).UniqueID(), token.Identity(retagged).UniqueID(),
			"tag 0x%02X: the retagged form is a distinct cache key", tag)

		require.Error(t, (&PolicyIdentity{}).Deserialize(retagged),
			"tag 0x%02X: a non-UTF8String spelling of the same policy must be rejected", tag)
	}
}

// policySignatureWithExtra is PolicySignature plus one undeclared element, the
// signature-envelope analog of policyIdentityWithExtra.
type policySignatureWithExtra struct {
	Signatures [][]byte
	Extra      int32
}

// TestExtraElementInsideSequenceRejectedOnSignatureFromBytes covers the same
// vector in PolicySignature.FromBytes, which PolicyVerifier.Verify calls
// directly on peer-supplied bytes. The nil slot mirrors an unsigned OR branch,
// which must keep decoding.
func TestExtraElementInsideSequenceRejectedOnSignatureFromBytes(t *testing.T) {
	sigs := [][]byte{[]byte("sig1"), nil, []byte("sig3")}

	canonical, err := (&PolicySignature{Signatures: sigs}).Bytes()
	require.NoError(t, err)
	require.NoError(t, (&PolicySignature{}).FromBytes(canonical),
		"the canonical encoding, unsigned slot included, must still deserialize")

	smuggled, err := asn1.Marshal(policySignatureWithExtra{Signatures: sigs, Extra: 7})
	require.NoError(t, err)

	require.Error(t, (&PolicySignature{}).FromBytes(smuggled),
		"a PolicySignature with an element smuggled inside the SEQUENCE must be rejected")
}

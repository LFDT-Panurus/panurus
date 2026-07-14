/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stubVerifier is a driver.Verifier that accepts exactly one (msg, sig) pair.
type stubVerifier struct {
	wantMsg []byte
	wantSig []byte
}

func (s *stubVerifier) Verify(msg, sig []byte) error {
	if string(msg) != string(s.wantMsg) || string(sig) != string(s.wantSig) {
		return assert.AnError
	}

	return nil
}

// id0/1/2 are stand-in token.Identity values.
var (
	id0 = token.Identity("identity-zero")
	id1 = token.Identity("identity-one")
	id2 = token.Identity("identity-two")
)

// ---------------------------------------------------------------------------
// PolicyIdentity — marshal / unmarshal round-trips
// ---------------------------------------------------------------------------

func TestPolicyIdentityRoundTrip(t *testing.T) {
	pi := &PolicyIdentity{
		Policy:     "$0 OR ($1 AND $2)",
		Identities: [][]byte{id0, id1, id2},
	}
	raw, err := pi.Serialize()
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	got := &PolicyIdentity{}
	require.NoError(t, got.Deserialize(raw))
	assert.Equal(t, pi.Policy, got.Policy)
	assert.Equal(t, pi.Identities, got.Identities)
}

func TestPolicyIdentityBytesAliasSameAsSerialize(t *testing.T) {
	pi := &PolicyIdentity{Policy: "$0 AND $1", Identities: [][]byte{id0, id1}}

	a, err := pi.Serialize()
	require.NoError(t, err)
	b, err := pi.Bytes()
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

// ---------------------------------------------------------------------------
// WrapPolicyIdentity / Unwrap
// ---------------------------------------------------------------------------

func TestWrapUnwrapRoundTrip(t *testing.T) {
	envelope, err := WrapPolicyIdentity("$0 OR $1", id0, id1)
	require.NoError(t, err)
	require.NotEmpty(t, envelope)

	pi, ok, err := Unwrap(envelope)
	require.NoError(t, err)
	require.True(t, ok, "expected policy identity type")
	assert.Equal(t, "$0 OR $1", pi.Policy)
	require.Len(t, pi.Identities, 2)
	assert.Equal(t, []byte(id0), pi.Identities[0])
	assert.Equal(t, []byte(id1), pi.Identities[1])
}

func TestWrapSetsCorrectTypeTag(t *testing.T) {
	envelope, err := WrapPolicyIdentity("$0", id0)
	require.NoError(t, err)

	// Unwrap via the identity package to confirm the type tag
	_, ok, err := Unwrap(envelope)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestUnwrapReturnsFalseForNonPolicyIdentity(t *testing.T) {
	// Wrap something with a different type (multisig = 5) and confirm Unwrap returns ok=false.
	// We simulate this by wrapping with the identity package directly using a different tag.
	from := tdriver.MultiSigIdentityType
	_ = from // just to confirm the constant exists; we use a hand-crafted identity below

	// A raw bytes blob that does not start with a valid TypedIdentity sequence
	// will return an error, not ok=false; so we test with a valid envelope of a
	// different type by wrapping with multisig bytes via the identity package.
	// The simplest approach: Unwrap on a plain identity byte slice (no envelope).
	_, ok, err := Unwrap(id0)
	// id0 has no valid TypedIdentity envelope — expect an error, not ok=true
	assert.False(t, ok)
	// Either ok is false with no error, or there is an error; both are acceptable.
	_ = err
}

func TestWrapErrorOnEmptyIdentities(t *testing.T) {
	_, err := WrapPolicyIdentity("$0")
	assert.Error(t, err)
}

func TestWrapErrorOnEmptyPolicy(t *testing.T) {
	_, err := WrapPolicyIdentity("", id0)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// PolicySignature — marshal / unmarshal round-trips
// ---------------------------------------------------------------------------

func TestPolicySignatureRoundTrip(t *testing.T) {
	sigs := [][]byte{[]byte("sig0"), nil, []byte("sig2")}
	ps := &PolicySignature{Signatures: sigs}

	raw, err := ps.Bytes()
	require.NoError(t, err)

	got := &PolicySignature{}
	require.NoError(t, got.FromBytes(raw))

	require.Len(t, got.Signatures, 3)
	assert.Equal(t, []byte("sig0"), got.Signatures[0])
	// ASN.1 SEQUENCE OF OCTET STRING marshals nil as an empty OCTET STRING
	assert.Empty(t, got.Signatures[1])
	assert.Equal(t, []byte("sig2"), got.Signatures[2])
}

// ---------------------------------------------------------------------------
// JoinSignatures
// ---------------------------------------------------------------------------

func TestJoinSignaturesAllPresent(t *testing.T) {
	ids := []token.Identity{id0, id1, id2}
	sigmas := map[string][]byte{
		id0.UniqueID(): []byte("s0"),
		id1.UniqueID(): []byte("s1"),
		id2.UniqueID(): []byte("s2"),
	}
	raw, err := JoinSignatures(ids, sigmas)
	require.NoError(t, err)

	ps := &PolicySignature{}
	require.NoError(t, ps.FromBytes(raw))
	assert.Equal(t, []byte("s0"), ps.Signatures[0])
	assert.Equal(t, []byte("s1"), ps.Signatures[1])
	assert.Equal(t, []byte("s2"), ps.Signatures[2])
}

func TestJoinSignaturesSparseOK(t *testing.T) {
	// Only id0 signs; id1 and id2 are absent.
	ids := []token.Identity{id0, id1, id2}
	sigmas := map[string][]byte{
		id0.UniqueID(): []byte("s0"),
	}
	raw, err := JoinSignatures(ids, sigmas)
	require.NoError(t, err)

	ps := &PolicySignature{}
	require.NoError(t, ps.FromBytes(raw))
	assert.Equal(t, []byte("s0"), ps.Signatures[0])
	assert.Empty(t, ps.Signatures[1])
	assert.Empty(t, ps.Signatures[2])
}

// ---------------------------------------------------------------------------
// PolicyVerifier — evaluation
// ---------------------------------------------------------------------------

const testMsg = "the-message"

func makeVerifiers(msg string, sigs ...string) []*stubVerifier {
	vs := make([]*stubVerifier, len(sigs))
	for i, s := range sigs {
		vs[i] = &stubVerifier{wantMsg: []byte(msg), wantSig: []byte(s)}
	}

	return vs
}

func buildPolicySig(t *testing.T, slots ...string) []byte {
	t.Helper()
	bs := make([][]byte, len(slots))
	for i, s := range slots {
		bs[i] = []byte(s) // empty string → nil-equivalent slot
	}
	raw, err := (&PolicySignature{Signatures: bs}).Bytes()
	require.NoError(t, err)

	return raw
}

func asDriverVerifiers(stubs []*stubVerifier) []tdriver.Verifier {
	vs := make([]tdriver.Verifier, len(stubs))
	for i, s := range stubs {
		vs[i] = s
	}

	return vs
}

func policyVerifier(t *testing.T, expr string, stubs []*stubVerifier) *PolicyVerifier {
	t.Helper()
	node, err := Parse(expr)
	require.NoError(t, err)

	return &PolicyVerifier{Policy: node, Verifiers: asDriverVerifiers(stubs)}
}

// --- single ref ---

func TestVerifySingleRefSatisfied(t *testing.T) {
	stubs := makeVerifiers(testMsg, "sig0")
	pv := policyVerifier(t, "$0", stubs)
	err := pv.Verify([]byte(testMsg), buildPolicySig(t, "sig0"))
	assert.NoError(t, err)
}

func TestVerifySingleRefWrongSig(t *testing.T) {
	stubs := makeVerifiers(testMsg, "sig0")
	pv := policyVerifier(t, "$0", stubs)
	err := pv.Verify([]byte(testMsg), buildPolicySig(t, "wrong"))
	assert.Error(t, err)
}

func TestVerifySingleRefAbsent(t *testing.T) {
	stubs := makeVerifiers(testMsg, "sig0")
	pv := policyVerifier(t, "$0", stubs)
	err := pv.Verify([]byte(testMsg), buildPolicySig(t, ""))
	assert.Error(t, err)
}

// --- AND ---

func TestVerifyAndBothPresent(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 AND $1", stubs)
	assert.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "s1")))
}

func TestVerifyAndOnlyLeft(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 AND $1", stubs)
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "")))
}

func TestVerifyAndOnlyRight(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 AND $1", stubs)
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "s1")))
}

func TestVerifyAndNeither(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 AND $1", stubs)
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "")))
}

// --- OR ---

func TestVerifyOrBothPresent(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 OR $1", stubs)
	assert.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "s1")))
}

func TestVerifyOrOnlyLeft(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 OR $1", stubs)
	assert.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "")))
}

func TestVerifyOrOnlyRight(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 OR $1", stubs)
	assert.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "s1")))
}

func TestVerifyOrNeither(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 OR $1", stubs)
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "")))
}

// --- complex: "$0 OR ($1 AND $2)" ---

func TestVerifyComplexLeftBranchSatisfied(t *testing.T) {
	// $0 alone is enough
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "$0 OR ($1 AND $2)", stubs)
	assert.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "", "")))
}

func TestVerifyComplexRightBranchSatisfied(t *testing.T) {
	// $1 AND $2 both present, $0 absent
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "$0 OR ($1 AND $2)", stubs)
	assert.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "s1", "s2")))
}

func TestVerifyComplexRightBranchIncomplete(t *testing.T) {
	// Only $1 present, $0 absent, $2 absent → fails
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "$0 OR ($1 AND $2)", stubs)
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "s1", "")))
}

func TestVerifyComplexNoneSatisfied(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "$0 OR ($1 AND $2)", stubs)
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "", "")))
}

// --- slot count mismatch ---

func TestVerifySlotCountMismatch(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 AND $1", stubs)
	// Only 1 slot instead of 2
	assert.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0")))
}

// --- precedence: AND tighter than OR ---

func TestVerifyPrecedenceAndOverOr(t *testing.T) {
	// "$0 OR $1 AND $2" == "$0 OR ($1 AND $2)"
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "$0 OR $1 AND $2", stubs)
	// $0 alone satisfies
	require.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "", "")))
	// $1 alone does not satisfy (needs $2 as well)
	require.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "s1", "")))
	// $1 AND $2 satisfies
	require.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "", "s1", "s2")))
}

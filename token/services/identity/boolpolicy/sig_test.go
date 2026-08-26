/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// sig_test.go contains focused unit tests for PolicyVerifier.Verify (sig.go).
// Each test group targets one logical requirement; helpers are shared with
// identity_test.go (same package).

package boolpolicy

import (
	"testing"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingVerifier wraps a driver.Verifier and records how many times Verify
// was invoked, so tests can assert per-index memoisation.
type countingVerifier struct {
	inner tdriver.Verifier
	calls int
}

func (c *countingVerifier) Verify(msg, sig []byte) error {
	c.calls++

	return c.inner.Verify(msg, sig)
}

// ---------------------------------------------------------------------------
// AND policy
// ---------------------------------------------------------------------------

// TestPolicyVerify_AND_BothSign verifies that a "$0 AND $1" policy passes
// when both slots carry valid signatures.
func TestPolicyVerify_AND_BothSign(t *testing.T) {
	stubs := makeVerifiers(testMsg, "alice", "bob")
	pv := policyVerifier(t, "$0 AND $1", stubs)

	sig := buildPolicySig(t, "alice", "bob")
	require.NoError(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_AND_OnlyLeftSigns verifies that a "$0 AND $1" policy fails
// when only slot 0 carries a valid signature.
func TestPolicyVerify_AND_OnlyLeftSigns(t *testing.T) {
	stubs := makeVerifiers(testMsg, "alice", "bob")
	pv := policyVerifier(t, "$0 AND $1", stubs)

	sig := buildPolicySig(t, "alice", "") // slot 1 absent
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_AND_OnlyRightSigns verifies that a "$0 AND $1" policy fails
// when only slot 1 carries a valid signature.
func TestPolicyVerify_AND_OnlyRightSigns(t *testing.T) {
	stubs := makeVerifiers(testMsg, "alice", "bob")
	pv := policyVerifier(t, "$0 AND $1", stubs)

	sig := buildPolicySig(t, "", "bob") // slot 0 absent
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// ---------------------------------------------------------------------------
// OR policy
// ---------------------------------------------------------------------------

// TestPolicyVerify_OR_OnlyLeftSigns verifies that "$0 OR $1" passes when only
// slot 0 is filled with a valid signature.
func TestPolicyVerify_OR_OnlyLeftSigns(t *testing.T) {
	stubs := makeVerifiers(testMsg, "alice", "bob")
	pv := policyVerifier(t, "$0 OR $1", stubs)

	sig := buildPolicySig(t, "alice", "") // slot 1 absent
	require.NoError(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_OR_OnlyRightSigns verifies that "$0 OR $1" passes when only
// slot 1 is filled with a valid signature.
func TestPolicyVerify_OR_OnlyRightSigns(t *testing.T) {
	stubs := makeVerifiers(testMsg, "alice", "bob")
	pv := policyVerifier(t, "$0 OR $1", stubs)

	sig := buildPolicySig(t, "", "bob") // slot 0 absent
	require.NoError(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_OR_NobodySigns verifies that "$0 OR $1" fails when neither
// slot contains a valid signature.
func TestPolicyVerify_OR_NobodySigns(t *testing.T) {
	stubs := makeVerifiers(testMsg, "alice", "bob")
	pv := policyVerifier(t, "$0 OR $1", stubs)

	sig := buildPolicySig(t, "", "") // both absent
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// ---------------------------------------------------------------------------
// Nested policy: ($0 OR $1) AND $2
// ---------------------------------------------------------------------------

// TestPolicyVerify_Nested_LeftOrAndThird verifies "($0 OR $1) AND $2" passes
// when $0 satisfies the OR branch and $2 satisfies the outer AND.
func TestPolicyVerify_Nested_LeftOrAndThird(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "($0 OR $1) AND $2", stubs)

	sig := buildPolicySig(t, "s0", "", "s2") // $1 absent
	require.NoError(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_Nested_RightOrAndThird verifies "($0 OR $1) AND $2" passes
// when $1 satisfies the OR branch and $2 satisfies the outer AND.
func TestPolicyVerify_Nested_RightOrAndThird(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "($0 OR $1) AND $2", stubs)

	sig := buildPolicySig(t, "", "s1", "s2") // $0 absent
	require.NoError(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_Nested_MissingThird verifies "($0 OR $1) AND $2" fails when
// the AND operand $2 is absent, even though the OR branch is satisfied.
func TestPolicyVerify_Nested_MissingThird(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "($0 OR $1) AND $2", stubs)

	sig := buildPolicySig(t, "s0", "s1", "") // $2 absent
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_Nested_NobodySigns verifies "($0 OR $1) AND $2" fails when
// no slot carries a valid signature.
func TestPolicyVerify_Nested_NobodySigns(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "($0 OR $1) AND $2", stubs)

	sig := buildPolicySig(t, "", "", "")
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// ---------------------------------------------------------------------------
// Wrong / corrupt signature bytes
// ---------------------------------------------------------------------------

// TestPolicyVerify_WrongSigValue verifies that Verify fails when a slot carries
// bytes that do not match the verifier's expected signature value.
func TestPolicyVerify_WrongSigValue(t *testing.T) {
	stubs := makeVerifiers(testMsg, "correct-sig")
	pv := policyVerifier(t, "$0", stubs)

	sig := buildPolicySig(t, "wrong-sig")
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_CorruptASN1 verifies that Verify fails fast when the outer
// PolicySignature envelope is not valid ASN.1.
func TestPolicyVerify_CorruptASN1(t *testing.T) {
	stubs := makeVerifiers(testMsg, "sig")
	pv := policyVerifier(t, "$0", stubs)

	err := pv.Verify([]byte(testMsg), []byte("not-asn1-at-all\xff\xfe"))
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Slot count mismatch
// ---------------------------------------------------------------------------

// TestPolicyVerify_SlotCountTooFew verifies that Verify returns an error when
// the PolicySignature has fewer slots than the number of component verifiers.
func TestPolicyVerify_SlotCountTooFew(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1", "s2")
	pv := policyVerifier(t, "$0 AND $1 AND $2", stubs)

	// Only 2 slots instead of 3
	sig := buildPolicySig(t, "s0", "s1")
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// TestPolicyVerify_SlotCountTooMany verifies that Verify returns an error when
// the PolicySignature has more slots than the number of component verifiers.
func TestPolicyVerify_SlotCountTooMany(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	pv := policyVerifier(t, "$0 AND $1", stubs)

	// 3 slots instead of 2
	sig := buildPolicySig(t, "s0", "s1", "extra")
	assert.Error(t, pv.Verify([]byte(testMsg), sig))
}

// ---------------------------------------------------------------------------
// Per-index memoisation within a single Verify call
// ---------------------------------------------------------------------------

// TestPolicyVerify_Memoisation_RepeatedRefVerifiedOnce verifies that an index
// referenced multiple times in a policy triggers the underlying verifier only
// once per Verify call.
func TestPolicyVerify_Memoisation_RepeatedRefVerifiedOnce(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")
	counter := &countingVerifier{inner: stubs[0]}

	// "$0 AND ($0 OR $1)" references $0 twice.
	node, err := Parse("$0 AND ($0 OR $1)")
	require.NoError(t, err)
	pv := &PolicyVerifier{
		Policy:    node,
		Verifiers: []tdriver.Verifier{counter, stubs[1]},
	}

	require.NoError(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0", "s1")))
	assert.Equal(t, 1, counter.calls, "verifier for $0 must be invoked exactly once")
}

// TestPolicyVerify_Memoisation_FailingRefVerifiedOnce verifies that a failing
// index is also cached: a wrong signature referenced twice is verified once and
// the cached failure is reused.
func TestPolicyVerify_Memoisation_FailingRefVerifiedOnce(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0")
	counter := &countingVerifier{inner: stubs[0]}

	// "$0 OR $0" references $0 twice; a wrong signature fails both times.
	node, err := Parse("$0 OR $0")
	require.NoError(t, err)
	pv := &PolicyVerifier{
		Policy:    node,
		Verifiers: []tdriver.Verifier{counter},
	}

	require.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "wrong")))
	assert.Equal(t, 1, counter.calls, "failing verifier for $0 must be invoked exactly once")
}

// ---------------------------------------------------------------------------
// Bounds checks at the point of use (issue #2073)
// ---------------------------------------------------------------------------

// TestEvalRef_ShorterVerifiersThanSignatures calls the AST evaluation directly with more signature
// slots than Verifiers. Verify rejects that mismatch before evalNode ever runs, so this exercises
// evalRef's own guard: a reference past the end of Verifiers must evaluate to false rather than
// panic with an index-out-of-range.
func TestEvalRef_ShorterVerifiersThanSignatures(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0")

	node, err := Parse("$0 OR $1")
	require.NoError(t, err)
	pv := &PolicyVerifier{
		Policy: node,
		// Two signature slots below, only one Verifier here.
		Verifiers: []tdriver.Verifier{stubs[0]},
	}

	sigs := [][]byte{[]byte("s0"), []byte("s1")}
	memo := make([]refResult, len(sigs))

	// $0 is in range and its signature verifies, so the OR is satisfied without reaching $1.
	assert.True(t, pv.evalNode(pv.Policy, []byte(testMsg), sigs, memo))

	// $1 alone is out of range for Verifiers and must simply not be satisfied.
	node, err = Parse("$1")
	require.NoError(t, err)
	pv.Policy = node
	memo = make([]refResult, len(sigs))
	assert.False(t, pv.evalNode(pv.Policy, []byte(testMsg), sigs, memo))
}

// TestEvalRef_ShorterMemoThanSignatures exercises the memo bounds check: memo is documented to have
// one entry per signature slot, and a shorter one must not panic.
func TestEvalRef_ShorterMemoThanSignatures(t *testing.T) {
	stubs := makeVerifiers(testMsg, "s0", "s1")

	node, err := Parse("$1")
	require.NoError(t, err)
	pv := &PolicyVerifier{Policy: node, Verifiers: []tdriver.Verifier{stubs[0], stubs[1]}}

	sigs := [][]byte{[]byte("s0"), []byte("s1")}
	assert.False(t, pv.evalNode(pv.Policy, []byte(testMsg), sigs, make([]refResult, 1)))
}

// TestEvalRef_NilVerifier covers a Verifiers slot left nil: the reference must fail instead of
// nil-dereferencing on Verify.
func TestEvalRef_NilVerifier(t *testing.T) {
	node, err := Parse("$0")
	require.NoError(t, err)
	pv := &PolicyVerifier{Policy: node, Verifiers: []tdriver.Verifier{nil}}

	sigs := [][]byte{[]byte("s0")}
	assert.False(t, pv.evalNode(pv.Policy, []byte(testMsg), sigs, make([]refResult, 1)))
	require.Error(t, pv.Verify([]byte(testMsg), buildPolicySig(t, "s0")))
}

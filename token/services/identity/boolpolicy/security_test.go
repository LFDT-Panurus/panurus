/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"context"
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

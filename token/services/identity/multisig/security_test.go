/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
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

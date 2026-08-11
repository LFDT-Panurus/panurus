/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig"
	htlc2 "github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

// These tests exercise the real production wiring in NewDeserializer, where the verifier multiplex
// is registered as its own component deserializer for multisig, boolpolicy and htlc. That
// self-registration is what makes the recursion unbounded in principle, so the bound has to be
// proven against the assembled multiplex and not only against the individual deserializers.

func wrapMultisig(t *testing.T, ids ...token.Identity) token.Identity {
	t.Helper()
	inner, err := (&multisig.MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)
	envelope, err := (&identity.TypedIdentity{Type: multisig.Multisig, Identity: inner}).Bytes()
	require.NoError(t, err)

	return envelope
}

func wrapPolicy(t *testing.T, ids ...token.Identity) token.Identity {
	t.Helper()
	raw := make([][]byte, len(ids))
	for k := range ids {
		raw[k] = ids[k]
	}
	inner, err := (&boolpolicy.PolicyIdentity{Policy: "$0", Identities: raw}).Bytes()
	require.NoError(t, err)
	envelope, err := (&identity.TypedIdentity{Type: boolpolicy.Policy, Identity: inner}).Bytes()
	require.NoError(t, err)

	return envelope
}

func wrapHTLC(t *testing.T, sender, recipient token.Identity) token.Identity {
	t.Helper()
	inner, err := json.Marshal(&htlc2.Script{Sender: sender, Recipient: recipient})
	require.NoError(t, err)
	envelope, err := (&identity.TypedIdentity{Type: htlc2.ScriptType, Identity: inner}).Bytes()
	require.NoError(t, err)

	return envelope
}

// A single-component multisig identity nested far past any plausible limit must be rejected rather
// than walked. This is the shape from the report: the cheapest bytes per level, so the deepest
// recursion for a given identity size.
func TestGetOwnerVerifier_RejectsDeeplyNestedMultisig(t *testing.T) {
	des := NewDeserializer()

	id := token.Identity("leaf")
	for range 512 {
		id = wrapMultisig(t, id)
	}

	_, err := des.GetOwnerVerifier(context.Background(), id)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// Alternating the composite types must not let an attacker spend a fresh budget at each type: the
// depth is counted per descent, not per type.
//
// The nesting here is shallower than in the multisig-only case on purpose: an htlc level carries its
// children as JSON, which base64-encodes them, so each htlc level inflates the identity by about a
// third and the size grows exponentially in the number of them. 24 levels is already far past any
// configurable bound.
func TestGetOwnerVerifier_RejectsDeeplyNestedMixedTypes(t *testing.T) {
	des := NewDeserializer()

	id := token.Identity("leaf")
	for i := range 24 {
		switch i % 3 {
		case 0:
			id = wrapMultisig(t, id)
		case 1:
			id = wrapPolicy(t, id)
		default:
			id = wrapHTLC(t, id, token.Identity("recipient-"+strconv.Itoa(i)))
		}
	}

	_, err := des.GetOwnerVerifier(context.Background(), id)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// A composite identity that fans out far more widely than the limit must be rejected before any
// component is deserialized, since depth alone does not bound fan-out.
func TestGetOwnerVerifier_RejectsWideFanOut(t *testing.T) {
	des := NewDeserializer()

	ids := make([]token.Identity, 4096)
	for k := range ids {
		ids[k] = token.Identity("identity-" + strconv.Itoa(k))
	}

	_, err := des.GetOwnerVerifier(context.Background(), wrapMultisig(t, ids...))
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)

	_, err = des.GetOwnerVerifier(context.Background(), wrapPolicy(t, ids...))
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

// Nesting within the limit must fail for a reason other than the bound: an x509 leaf is not
// available here, so the descent is expected to reach the leaf and fail there. What matters is that
// it is *not* rejected as over-nested, which is what proves the bound does not fire on real shapes.
func TestGetOwnerVerifier_AllowsRealisticNesting(t *testing.T) {
	des := NewDeserializer()

	// policy over multisig over a leaf identity - three levels, the deepest shape described as
	// realistic in the report
	id := wrapPolicy(t, wrapMultisig(t, token.Identity("leaf")))

	_, err := des.GetOwnerVerifier(context.Background(), id)
	require.Error(t, err, "the leaf identity is not a valid x509 identity, so the descent still fails")
	require.NotErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep,
		"a three-level identity must not be rejected as over-nested")
	require.NotErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

// The bound also has to hold on the matcher path, which recurses independently of verifier
// deserialization. The audit info has to be nested in step with the identity, otherwise the descent
// stops early on a component-count mismatch and never reaches the depth being tested.
//
// The nesting is shallower than in the verifier case because each level base64-encodes the level
// below it in JSON, so the size grows exponentially. 24 levels is far past any configurable bound.
func TestMatchIdentity_RejectsDeeplyNestedIdentity(t *testing.T) {
	des := NewDeserializer()

	id := token.Identity("leaf")
	auditInfo := []byte("leaf-audit-info")
	for range 24 {
		id = wrapMultisig(t, id)
		nested, err := json.Marshal(&multisig.AuditInfo{
			IdentityAuditInfos: []multisig.IdentityAuditInfo{{AuditInfo: auditInfo}},
		})
		require.NoError(t, err)
		auditInfo = nested
	}

	err := des.MatchIdentity(context.Background(), id, auditInfo)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// FuzzOwnerVerifierNoPanic drives the owner-identity deserialization path, the one reached from the
// transfer validator once per input token before any signature is checked, with arbitrary bytes. It
// must always return - never panic, and never recurse without bound.
func FuzzOwnerVerifierNoPanic(f *testing.F) {
	nest := func(depth int) []byte {
		id := []byte("leaf")
		for range depth {
			inner, err := (&multisig.MultiIdentity{Identities: []token.Identity{id}}).Bytes()
			if err != nil {
				return id
			}
			envelope, err := (&identity.TypedIdentity{Type: multisig.Multisig, Identity: inner}).Bytes()
			if err != nil {
				return id
			}
			id = envelope
		}

		return id
	}

	f.Add([]byte{})
	f.Add([]byte("not an identity"))
	f.Add(nest(1))
	f.Add(nest(3))
	// the shape from the report: nesting far past any plausible bound
	f.Add(nest(64))
	f.Add(nest(600))
	// a truncated deep identity, so the fuzzer starts from partially valid DER as well
	if deep := nest(64); len(deep) > 8 {
		f.Add(deep[:len(deep)/2])
	}

	des := NewDeserializer()
	f.Fuzz(func(t *testing.T, raw []byte) {
		// the contract is only that this returns; a verifier or an error are both fine
		_, _ = des.GetOwnerVerifier(context.Background(), raw)
		_ = des.MatchIdentity(context.Background(), raw, []byte(`{"IdentityAuditInfos":[{}]}`))
		_, _ = des.Recipients(raw)
	})
}

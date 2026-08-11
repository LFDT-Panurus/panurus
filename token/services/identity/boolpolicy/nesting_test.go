/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"context"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recursiveVerifierDES re-enters the deserializer under test for any component that is itself a
// policy identity, reproducing the production wiring where the component deserializer is the parent
// multiplex the policy deserializer is registered in.
type recursiveVerifierDES struct {
	d     *TypedIdentityDeserializer
	calls int
}

func (r *recursiveVerifierDES) DeserializeVerifier(ctx context.Context, id token.Identity) (tdriver.Verifier, error) {
	r.calls++
	ti, err := identity.UnmarshalTypedIdentity(id)
	if err != nil || ti.Type != Policy {
		return &stubVerifier{}, nil //nolint:nilerr // a non-policy component is the leaf
	}

	return r.d.DeserializeVerifier(ctx, Policy, ti.Identity)
}

type stubMatcherProvider struct {
	calls int
}

func (s *stubMatcherProvider) GetAuditInfoMatcher(context.Context, token.Identity, []byte) (tdriver.Matcher, error) {
	s.calls++

	return &stubMatcher{}, nil
}

type stubMatcher struct{}

func (stubMatcher) Match(context.Context, []byte) error { return nil }

// distinctIdentities returns n distinct component identities, so that a test aimed at the fan-out
// bound is not short-circuited by the duplicate-component check.
func distinctIdentities(n int) [][]byte {
	ids := make([][]byte, n)
	for k := range ids {
		ids[k] = []byte("identity-" + strconv.Itoa(k))
	}

	return ids
}

// wrapPolicy envelopes ids as a policy TypedIdentity without going through WrapPolicyIdentity, so
// that a test can build the identities an attacker would craft directly on the wire.
func wrapPolicy(t *testing.T, ids ...[]byte) token.Identity {
	t.Helper()
	inner, err := (&PolicyIdentity{Policy: "$0", Identities: ids}).Bytes()
	require.NoError(t, err)
	envelope, err := (&identity.TypedIdentity{Type: Policy, Identity: inner}).Bytes()
	require.NoError(t, err)

	return envelope
}

// nestedPolicy returns a policy identity nested depth levels deep around leaf.
func nestedPolicy(t *testing.T, depth int, leaf []byte) token.Identity {
	t.Helper()
	id := token.Identity(leaf)
	for range depth {
		id = wrapPolicy(t, id)
	}

	return id
}

// A single policy identity carrying more components than MaxIdentityComponents must be rejected: one
// level of nesting that fans out widely is a single recursive step doing unbounded work.
func TestDeserializeVerifier_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	des := &recursiveVerifierDES{}
	d := NewTypedIdentityDeserializer(des, nil)
	des.d = d

	raw, err := (&PolicyIdentity{Policy: "$0", Identities: distinctIdentities(maxComponents + 1)}).Bytes()
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Policy, raw)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
	assert.Zero(t, des.calls, "the fan-out bound must be enforced before any component is deserialized")
}

func TestDeserializeVerifier_AllowsFanOutAtTheLimit(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	des := &recursiveVerifierDES{}
	d := NewTypedIdentityDeserializer(des, nil)
	des.d = d

	raw, err := (&PolicyIdentity{Policy: "$0", Identities: distinctIdentities(maxComponents)}).Bytes()
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Policy, raw)
	require.NoError(t, err)
	assert.Equal(t, maxComponents, des.calls)
}

func TestGetAuditInfoMatcher_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	provider := &stubMatcherProvider{}
	d := NewTypedIdentityDeserializer(nil, provider)

	ids := distinctIdentities(maxComponents + 1)
	infos := make([]IdentityAuditInfo, len(ids))
	for k := range infos {
		infos[k].AuditInfo = []byte("audit-info")
	}
	auditInfo, err := (&AuditInfo{IdentityAuditInfos: infos}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfoMatcher(ctx, wrapPolicy(t, ids...), auditInfo)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
	assert.Zero(t, provider.calls, "the fan-out bound must be enforced before any component matcher is built")
}

// The depth bound must hold on the verifier path: an identity nested past the limit is rejected
// rather than walked.
func TestDeserializeVerifier_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 3
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	des := &recursiveVerifierDES{}
	d := NewTypedIdentityDeserializer(des, nil)
	des.d = d

	deep := nestedPolicy(t, maxDepth+2, []byte("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Policy, ti.Identity)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// Nesting up to the limit must still work - a policy over a multisig over an x509 identity is a real
// shape, so the bound has to reject only what exceeds it.
func TestDeserializeVerifier_AllowsDepthAtTheLimit(t *testing.T) {
	const maxDepth = 3
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	des := &recursiveVerifierDES{}
	d := NewTypedIdentityDeserializer(des, nil)
	des.d = d

	nested := nestedPolicy(t, maxDepth, []byte("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(nested)
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Policy, ti.Identity)
	require.NoError(t, err)
}

// InfoMatcher.Match recurses separately from the deserialization that built it, so it needs its own
// bound.
func TestInfoMatcher_Match_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 2
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	var m *InfoMatcher
	m = &InfoMatcher{AuditInfoMatcher: []tdriver.Matcher{matcherFunc(func(ctx context.Context, raw []byte) error {
		ti, err := identity.UnmarshalTypedIdentity(raw)
		if err != nil || ti.Type != Policy {
			return nil //nolint:nilerr // a non-policy component is the leaf
		}

		return m.Match(ctx, ti.Identity)
	})}}

	deep := nestedPolicy(t, maxDepth+2, []byte("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	require.ErrorIs(t, m.Match(ctx, ti.Identity), tdriver.ErrIdentityNestingTooDeep)
}

type matcherFunc func(context.Context, []byte) error

func (f matcherFunc) Match(ctx context.Context, raw []byte) error { return f(ctx, raw) }

// A context that no validator seeded must still be bounded, by the defaults.
func TestDeserializeVerifier_UnseededContextIsStillBounded(t *testing.T) {
	des := &recursiveVerifierDES{}
	d := NewTypedIdentityDeserializer(des, nil)
	des.d = d

	deep := nestedPolicy(t, tdriver.DefaultResourceLimits().MaxIdentityDepth+2, []byte("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(context.Background(), Policy, ti.Identity)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// WrapPolicyIdentity is the honest-caller path and holds to the default fan-out bound, so that an
// identity built in-process cannot exceed what a validator will later accept.
func TestWrapPolicyIdentity_RejectsExcessiveFanOut(t *testing.T) {
	max := tdriver.DefaultResourceLimits().MaxIdentityComponents

	toIdentities := func(raw [][]byte) []token.Identity {
		ids := make([]token.Identity, len(raw))
		for k := range raw {
			ids[k] = raw[k]
		}

		return ids
	}

	_, err := WrapPolicyIdentity("$0", toIdentities(distinctIdentities(max))...)
	require.NoError(t, err, "a component count at the limit must be accepted")

	_, err = WrapPolicyIdentity("$0", toIdentities(distinctIdentities(max+1))...)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

// stubAuditInfoProvider is a driver.AuditInfoProvider that records its lookups and, optionally,
// resolves a composite identity by re-entering the deserializer the way the production provider does.
type stubAuditInfoProvider struct {
	calls int
	fn    func(context.Context, tdriver.Identity) ([]byte, error)
}

func (s *stubAuditInfoProvider) GetAuditInfo(ctx context.Context, id tdriver.Identity) ([]byte, error) {
	s.calls++
	if s.fn != nil {
		return s.fn(ctx, id)
	}

	return nil, nil
}

// The fan-out bound must be enforced on the audit-info collection path too. It resolves one provider
// lookup per component, and a composite component descends further, so a single level that fans out
// without limit is unbounded work that the depth bound does not cover.
func TestGetAuditInfo_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	p := &stubAuditInfoProvider{}
	d := NewTypedIdentityDeserializer(nil, nil)

	raw, err := (&PolicyIdentity{Policy: "$0", Identities: distinctIdentities(maxComponents + 1)}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfo(ctx, token.Identity("owner"), Policy, raw, p)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
	assert.Equal(t, 1, p.calls,
		"only the cache probe for the composite identity itself may run before the bound is enforced")
}

func TestGetAuditInfo_AllowsFanOutAtTheLimit(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	p := &stubAuditInfoProvider{}
	d := NewTypedIdentityDeserializer(nil, nil)

	raw, err := (&PolicyIdentity{Policy: "$0", Identities: distinctIdentities(maxComponents)}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfo(ctx, token.Identity("owner"), Policy, raw, p)
	require.NoError(t, err)
	assert.Equal(t, 1+maxComponents, p.calls)
}

// The depth bound holds on the audit-info collection path as well: the provider resolves an unstored
// composite component by re-entering this deserializer, which is the recursion the bound covers.
func TestGetAuditInfo_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 3
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	d := NewTypedIdentityDeserializer(nil, nil)
	p := &stubAuditInfoProvider{}
	// the guard keeps the cache probe for an identity already being resolved from looping back into
	// it; production has the same shape, the provider only descends into components
	resolving := map[string]bool{}
	p.fn = func(ctx context.Context, id tdriver.Identity) ([]byte, error) {
		if resolving[string(id)] {
			return nil, nil
		}
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil || ti.Type != Policy {
			return nil, nil //nolint:nilerr // a non-policy component is the leaf, nothing stored
		}
		resolving[string(id)] = true

		return d.GetAuditInfo(ctx, id, Policy, ti.Identity, p)
	}

	deep := nestedPolicy(t, maxDepth+2, []byte("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	_, err = d.GetAuditInfo(ctx, deep, Policy, ti.Identity, p)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// nestedPolicyAuditInfo returns a policy identity nested depth levels deep around a single x509 leaf
// carrying eid, together with the matching nested composite audit info.
func nestedPolicyAuditInfo(t *testing.T, depth int, eid string) (token.Identity, []byte) {
	t.Helper()
	member, info := newX509Member(t, "cert-leaf", eid)
	id := token.Identity(member)
	for range depth {
		id, info = newPolicyIdentity(t, "$0", [][]byte{id}, [][]byte{info})
	}

	return id, info
}

// Deriving the common enrollment ID resolves one component audit info per component through the
// parent multiplex, so this path needs the fan-out bound just like the other component-resolving
// steps. It is reachable from an auditor inspecting an untrusted owner identity.
func TestPolicyEnrollmentID_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	members := make([][]byte, maxComponents+1)
	infos := make([][]byte, len(members))
	for k := range members {
		members[k], infos[k] = newX509Member(t, "cert-"+strconv.Itoa(k), "wallet-42")
	}
	policyID, wrapped := newPolicyIdentity(t, "$0", members, infos)

	_, _, err := newEIDRHDeserializer().GetEIDAndRH(ctx, policyID, wrapped)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

// The depth bound holds on the same path: a component's audit info is resolved through the parent
// multiplex, which re-enters this deserializer for a nested policy component.
func TestPolicyEnrollmentID_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 3
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	policyID, wrapped := nestedPolicyAuditInfo(t, maxDepth+2, "wallet-42")

	_, _, err := newEIDRHDeserializer().GetEIDAndRH(ctx, policyID, wrapped)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// A chain that nests exactly to the limit still resolves, so the bound cannot be satisfied by an
// off-by-one that rejects legitimate identities.
func TestPolicyEnrollmentID_AllowsDepthAtTheLimit(t *testing.T) {
	const maxDepth = 3
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	policyID, wrapped := nestedPolicyAuditInfo(t, maxDepth, "wallet-42")

	eid, _, err := newEIDRHDeserializer().GetEIDAndRH(ctx, policyID, wrapped)
	require.NoError(t, err)
	assert.Equal(t, "wallet-42", eid)
}

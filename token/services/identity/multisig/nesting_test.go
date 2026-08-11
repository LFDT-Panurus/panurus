/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/multisig/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// distinctIdentities returns n component identities that are distinct, so that a test aimed at the
// fan-out bound is not short-circuited by the duplicate-component check.
func distinctIdentities(n int) []token.Identity {
	ids := make([]token.Identity, n)
	for k := range ids {
		ids[k] = token.Identity("identity-" + strconv.Itoa(k))
	}

	return ids
}

// wrapMultisig envelopes ids as a multisig TypedIdentity without going through WrapIdentities, so
// that a test can build the identities an attacker would craft directly on the wire - which is the
// only surface that matters here, since WrapIdentities is the honest-caller path.
func wrapMultisig(t *testing.T, ids ...token.Identity) token.Identity {
	t.Helper()
	inner, err := (&MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)
	envelope, err := (&identity.TypedIdentity{Type: Multisig, Identity: inner}).Bytes()
	require.NoError(t, err)

	return envelope
}

// nestedMultisig returns a multisig identity nested depth levels deep around leaf. Each level holds
// a single component, which is the cheapest shape per level and therefore the one that reaches the
// greatest depth for a given identity size.
func nestedMultisig(t *testing.T, depth int, leaf token.Identity) token.Identity {
	t.Helper()
	id := leaf
	for range depth {
		id = wrapMultisig(t, id)
	}

	return id
}

// A single multisig identity carrying more components than MaxIdentityComponents must be rejected.
// The depth bound does not cover this: one level of nesting that fans out to thousands of components
// is a single recursive step that does an unbounded amount of work.
func TestDeserializeVerifier_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	verifierDES := &mock.VerifierDES{}
	verifierDES.DeserializeVerifierReturns(&mock.Verifier{}, nil)
	d := NewTypedIdentityDeserializer(verifierDES, nil)

	raw, err := (&MultiIdentity{Identities: distinctIdentities(maxComponents + 1)}).Bytes()
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Multisig, raw)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
	assert.Zero(t, verifierDES.DeserializeVerifierCallCount(),
		"the fan-out bound must be enforced before any component is deserialized")
}

// A component count exactly at the limit must still be accepted - an off-by-one here would break
// legitimate multisig identities.
func TestDeserializeVerifier_AllowsFanOutAtTheLimit(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	verifierDES := &mock.VerifierDES{}
	verifierDES.DeserializeVerifierReturns(&mock.Verifier{}, nil)
	d := NewTypedIdentityDeserializer(verifierDES, nil)

	raw, err := (&MultiIdentity{Identities: distinctIdentities(maxComponents)}).Bytes()
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Multisig, raw)
	require.NoError(t, err)
	assert.Equal(t, maxComponents, verifierDES.DeserializeVerifierCallCount())
}

// The fan-out bound must be enforced on the matcher-construction path too, not only when building
// verifiers - it recurses into the components just the same.
func TestGetAuditInfoMatcher_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	matcher := &mock.AuditInfoMatcher{}
	matcher.GetAuditInfoMatcherReturns(&mock.Matcher{}, nil)
	d := NewTypedIdentityDeserializer(nil, matcher)

	ids := distinctIdentities(maxComponents + 1)
	infos := make([]IdentityAuditInfo, len(ids))
	for k := range infos {
		infos[k].AuditInfo = []byte("audit-info")
	}
	auditInfo, err := (&AuditInfo{IdentityAuditInfos: infos}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfoMatcher(ctx, wrapMultisig(t, ids...), auditInfo)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
	assert.Zero(t, matcher.GetAuditInfoMatcherCallCount(),
		"the fan-out bound must be enforced before any component matcher is built")
}

// The depth bound must hold on the verifier path. The component deserializer here is the parent
// multiplex in production, so an identity nested past the limit is rejected rather than walked.
func TestDeserializeVerifier_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 3

	verifierDES := &mock.VerifierDES{}
	d := NewTypedIdentityDeserializer(verifierDES, nil)

	// each recursive step re-enters this same deserializer, as the production multiplex wiring does
	verifierDES.DeserializeVerifierCalls(func(ctx context.Context, id token.Identity) (tdriver.Verifier, error) {
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil || ti.Type != Multisig {
			return &mock.Verifier{}, nil //nolint:nilerr // a non-multisig component is the leaf
		}

		return d.DeserializeVerifier(ctx, Multisig, ti.Identity)
	})

	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)
	deep := nestedMultisig(t, maxDepth+2, token.Identity("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Multisig, ti.Identity)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// Nesting up to the limit must still work: a policy over a multisig over an x509 identity is a real
// shape, so the bound has to reject only what exceeds it.
func TestDeserializeVerifier_AllowsDepthAtTheLimit(t *testing.T) {
	const maxDepth = 3

	verifierDES := &mock.VerifierDES{}
	d := NewTypedIdentityDeserializer(verifierDES, nil)
	verifierDES.DeserializeVerifierCalls(func(ctx context.Context, id token.Identity) (tdriver.Verifier, error) {
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil || ti.Type != Multisig {
			return &mock.Verifier{}, nil //nolint:nilerr // a non-multisig component is the leaf
		}

		return d.DeserializeVerifier(ctx, Multisig, ti.Identity)
	})

	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)
	nested := nestedMultisig(t, maxDepth, token.Identity("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(nested)
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(ctx, Multisig, ti.Identity)
	require.NoError(t, err)
}

// InfoMatcher.Match recurses separately from the deserialization that built it, so it needs its own
// bound. Without one, an identity that was cheap to deserialize could still drive deep recursion at
// match time.
func TestInfoMatcher_Match_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 2
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	// a matcher that recurses into itself, mirroring a nested multisig identity's matcher tree
	var m *InfoMatcher
	inner := &mock.Matcher{}
	inner.MatchCalls(func(ctx context.Context, raw []byte) error {
		ti, err := identity.UnmarshalTypedIdentity(raw)
		if err != nil || ti.Type != Multisig {
			return nil //nolint:nilerr // a non-multisig component is the leaf
		}

		return m.Match(ctx, ti.Identity)
	})
	m = &InfoMatcher{AuditInfoMatcher: []tdriver.Matcher{inner}}

	deep := nestedMultisig(t, maxDepth+2, token.Identity("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	require.ErrorIs(t, m.Match(ctx, ti.Identity), tdriver.ErrIdentityNestingTooDeep)
}

// A context that no validator seeded must still be bounded, by the defaults - deserialization is
// reachable from wallet and auditor paths that never carry ResourceLimits.
func TestDeserializeVerifier_UnseededContextIsStillBounded(t *testing.T) {
	verifierDES := &mock.VerifierDES{}
	d := NewTypedIdentityDeserializer(verifierDES, nil)
	verifierDES.DeserializeVerifierCalls(func(ctx context.Context, id token.Identity) (tdriver.Verifier, error) {
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil || ti.Type != Multisig {
			return &mock.Verifier{}, nil //nolint:nilerr // a non-multisig component is the leaf
		}

		return d.DeserializeVerifier(ctx, Multisig, ti.Identity)
	})

	deep := nestedMultisig(t, tdriver.DefaultResourceLimits().MaxIdentityDepth+2, token.Identity("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	_, err = d.DeserializeVerifier(context.Background(), Multisig, ti.Identity)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

// WrapIdentities is the honest-caller path and holds to the default fan-out bound, so that an
// identity built in-process cannot exceed what a validator will later accept.
func TestWrapIdentities_RejectsExcessiveFanOut(t *testing.T) {
	max := tdriver.DefaultResourceLimits().MaxIdentityComponents

	_, err := WrapIdentities(distinctIdentities(max)...)
	require.NoError(t, err, "a component count at the limit must be accepted")

	_, err = WrapIdentities(distinctIdentities(max + 1)...)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
}

// The fan-out bound must be enforced on the audit-info collection path too. It resolves one provider
// lookup per component, and a composite component descends further, so a single level that fans out
// without limit is unbounded work that the depth bound does not cover.
func TestGetAuditInfo_RejectsExcessiveFanOut(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	p := &mock.AuditInfoProvider{}
	// nothing stored for the composite identity itself, so it is assembled from its components
	p.GetAuditInfoReturns([]byte("audit-info"), nil)
	p.GetAuditInfoReturnsOnCall(0, nil, nil)

	d := NewTypedIdentityDeserializer(nil, nil)
	raw, err := (&MultiIdentity{Identities: distinctIdentities(maxComponents + 1)}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfo(ctx, token.Identity("owner"), Multisig, raw, p)
	require.ErrorIs(t, err, tdriver.ErrTooManyIdentityComponents)
	assert.Equal(t, 1, p.GetAuditInfoCallCount(),
		"only the cache probe for the composite identity itself may run before the bound is enforced")
}

func TestGetAuditInfo_AllowsFanOutAtTheLimit(t *testing.T) {
	const maxComponents = 4
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), 5, maxComponents)

	p := &mock.AuditInfoProvider{}
	p.GetAuditInfoReturns([]byte("audit-info"), nil)
	p.GetAuditInfoReturnsOnCall(0, nil, nil)

	d := NewTypedIdentityDeserializer(nil, nil)
	raw, err := (&MultiIdentity{Identities: distinctIdentities(maxComponents)}).Bytes()
	require.NoError(t, err)

	_, err = d.GetAuditInfo(ctx, token.Identity("owner"), Multisig, raw, p)
	require.NoError(t, err)
	assert.Equal(t, 1+maxComponents, p.GetAuditInfoCallCount())
}

// The depth bound holds on the audit-info collection path as well: the provider resolves an unstored
// composite component by re-entering this deserializer, which is the recursion the bound covers.
func TestGetAuditInfo_RejectsExcessiveDepth(t *testing.T) {
	const maxDepth = 3
	ctx := tdriver.WithIdentityNestingLimits(context.Background(), maxDepth, 16)

	p := &mock.AuditInfoProvider{}
	d := NewTypedIdentityDeserializer(nil, nil)
	// a provider that has nothing stored and resolves a composite identity through the deserializer.
	// The guard keeps the cache probe for an identity already being resolved from looping back into
	// it; production has the same shape, the provider only descends into components.
	resolving := map[string]bool{}
	p.GetAuditInfoCalls(func(ctx context.Context, id tdriver.Identity) ([]byte, error) {
		if resolving[string(id)] {
			return nil, nil
		}
		ti, err := identity.UnmarshalTypedIdentity(id)
		if err != nil || ti.Type != Multisig {
			return nil, nil //nolint:nilerr // a non-multisig component is the leaf, nothing stored
		}
		resolving[string(id)] = true

		return d.GetAuditInfo(ctx, id, Multisig, ti.Identity, p)
	})

	deep := nestedMultisig(t, maxDepth+2, token.Identity("leaf"))
	ti, err := identity.UnmarshalTypedIdentity(deep)
	require.NoError(t, err)

	_, err = d.GetAuditInfo(ctx, deep, Multisig, ti.Identity, p)
	require.ErrorIs(t, err, tdriver.ErrIdentityNestingTooDeep)
}

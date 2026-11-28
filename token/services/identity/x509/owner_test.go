/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package x509

import (
	"crypto/x509"
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownerFixture holds an owner root, a certificate it issued and a forged self-signed one, both for "victim".
type ownerFixture struct {
	root    *crypto.TestCertificate
	legit   *crypto.TestCertificate
	forged  *crypto.TestCertificate
	checker *OwnerChainChecker
}

func newOwnerFixture(t *testing.T, exempt ...driver.Identity) *ownerFixture {
	t.Helper()
	root := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("root"), nil)
	checker, err := NewOwnerChainChecker([][]byte{root.PEM}, nil, exempt)
	require.NoError(t, err)
	require.NotNil(t, checker)

	return &ownerFixture{
		root:    root,
		legit:   crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("victim"), root),
		forged:  crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("victim"), nil),
		checker: checker,
	}
}

// auditInfoOf returns the audit info that cert generates for itself, with the given EID.
func auditInfoOf(t *testing.T, cert *crypto.TestCertificate, eid string) []byte {
	t.Helper()
	rh, err := crypto.RevocationHandleFromCert(cert.Cert)
	require.NoError(t, err)
	raw, err := (&AuditInfo{EID: eid, RH: rh}).Bytes()
	require.NoError(t, err)

	return raw
}

func TestNewOwnerChainChecker(t *testing.T) {
	c, err := NewOwnerChainChecker(nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, c)

	root := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("root"), nil)
	_, err = NewOwnerChainChecker(nil, [][]byte{root.PEM}, nil)
	require.ErrorContains(t, err, "intermediates set without roots")
	_, err = NewOwnerChainChecker([][]byte{[]byte("garbage")}, nil, nil)
	require.ErrorContains(t, err, "invalid x509 owner certificates")

	// exempt entries that are not typed x509 identities are ignored
	idemix, err := identity.WrapWithType(driver.IdemixIdentityType, root.PEM)
	require.NoError(t, err)
	c, err = NewOwnerChainChecker([][]byte{root.PEM}, nil, []driver.Identity{nil, []byte("untyped"), idemix})
	require.NoError(t, err)
	assert.Empty(t, c.exempt)
}

func TestOwnerChainChecker_Bind(t *testing.T) {
	issuer := crypto.NewTestCertificate(t, crypto.NewTestLeafTemplate("issuer"), nil)
	issuerCA := crypto.NewTestCertificate(t, crypto.NewTestCATemplate("issuer-ca"), nil)
	typedIssuer, err := identity.WrapWithType(IdentityType, issuer.PEM)
	require.NoError(t, err)
	typedIssuerCA, err := identity.WrapWithType(IdentityType, issuerCA.PEM)
	require.NoError(t, err)
	f := newOwnerFixture(t, typedIssuer, typedIssuerCA)
	rh, err := crypto.RevocationHandleFromCert(f.legit.Cert)
	require.NoError(t, err)

	require.NoError(t, f.checker.bind(f.legit.PEM, "victim", rh))
	// exempt identities skip the chain and leaf-CA rules
	issuerRH, err := crypto.RevocationHandleFromCert(issuer.Cert)
	require.NoError(t, err)
	require.NoError(t, f.checker.bind(issuer.PEM, "issuer", issuerRH))
	issuerCARH, err := crypto.RevocationHandleFromCert(issuerCA.Cert)
	require.NoError(t, err)
	require.NoError(t, f.checker.bind(issuerCA.PEM, "issuer-ca", issuerCARH))

	tests := []struct {
		name string
		raw  []byte
		eid  string
		rh   []byte
		err  string
	}{
		{name: "forged", raw: f.forged.PEM, eid: "victim", rh: rh, err: "does not chain"},
		{name: "EID mismatch", raw: f.legit.PEM, eid: "alice", rh: rh, err: "expected enrollment ID [alice], got [victim]"},
		{name: "RH mismatch", raw: f.legit.PEM, eid: "victim", rh: []byte("rh"), err: "revocation handle does not match"},
		{name: "root as owner", raw: f.root.PEM, eid: "root", err: "is a CA"},
		{name: "trailing bytes", raw: append(append([]byte{}, f.legit.PEM...), 'x'), eid: "victim", rh: rh, err: "trailing bytes"},
		{name: "garbage", raw: []byte("garbage"), eid: "victim", rh: rh, err: "expected exactly one PEM block"},
		{name: "exempt EID mismatch", raw: issuer.PEM, eid: "victim", rh: issuerRH, err: "expected enrollment ID"},
		{name: "exempt RH mismatch", raw: issuer.PEM, eid: "issuer", rh: rh, err: "revocation handle does not match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := f.checker.bind(tc.raw, tc.eid, tc.rh)
			require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
			require.NotErrorIs(t, err, identity.ErrUnresolvableIdentity)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestAuditInfoMatcher_WithChecker(t *testing.T) {
	f := newOwnerFixture(t)
	des := &AuditMatcherDeserializer{Checker: f.checker}

	m, err := des.GetAuditInfoMatcher(t.Context(), nil, auditInfoOf(t, f.legit, "victim"))
	require.NoError(t, err)
	require.NoError(t, m.Match(t.Context(), f.legit.PEM))

	tests := []struct {
		name      string
		id        []byte
		auditInfo []byte
	}{
		{name: "forged with its own RH", id: f.forged.PEM, auditInfo: auditInfoOf(t, f.forged, "victim")},
		{name: "EID mismatch", id: f.legit.PEM, auditInfo: auditInfoOf(t, f.legit, "alice")},
		{name: "RH mismatch", id: f.legit.PEM, auditInfo: auditInfoOf(t, f.forged, "victim")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := des.GetAuditInfoMatcher(t.Context(), nil, tc.auditInfo)
			require.NoError(t, err)
			require.ErrorIs(t, m.Match(t.Context(), tc.id), identity.ErrUntrustedIdentity)
		})
	}
}

func TestAuditInfoDeserializer_WithChecker(t *testing.T) {
	f := newOwnerFixture(t)
	des := &AuditInfoDeserializer{Checker: f.checker}

	ai, err := des.DeserializeAuditInfo(t.Context(), f.legit.PEM, auditInfoOf(t, f.legit, "victim"))
	require.NoError(t, err)
	assert.Equal(t, "victim", ai.EnrollmentID())

	tests := []struct {
		name      string
		id        []byte
		auditInfo []byte
	}{
		{name: "forged with its own RH", id: f.forged.PEM, auditInfo: auditInfoOf(t, f.forged, "victim")},
		{name: "EID mismatch", id: f.legit.PEM, auditInfo: auditInfoOf(t, f.legit, "alice")},
		{name: "RH mismatch", id: f.legit.PEM, auditInfo: auditInfoOf(t, f.forged, "victim")},
		{name: "no identity", id: nil, auditInfo: auditInfoOf(t, f.legit, "victim")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := des.DeserializeAuditInfo(t.Context(), tc.id, tc.auditInfo)
			require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
		})
	}
	_, err = des.DeserializeAuditInfo(t.Context(), f.legit.PEM, []byte("invalid"))
	require.Error(t, err)
	require.NotErrorIs(t, err, identity.ErrUntrustedIdentity)
}

// TestZeroValue_Unchanged pins that the zero-value structs check neither the chain nor the binding.
func TestZeroValue_Unchanged(t *testing.T) {
	f := newOwnerFixture(t)
	forgedInfo := auditInfoOf(t, f.legit, "victim")

	m, err := (&AuditMatcherDeserializer{}).GetAuditInfoMatcher(t.Context(), nil, forgedInfo)
	require.NoError(t, err)
	require.NoError(t, m.Match(t.Context(), f.forged.PEM))

	ai, err := (&AuditInfoDeserializer{}).DeserializeAuditInfo(t.Context(), f.forged.PEM, forgedInfo)
	require.NoError(t, err)
	assert.Equal(t, "victim", ai.EnrollmentID())
	ai, err = (&AuditInfoDeserializer{}).DeserializeAuditInfo(t.Context(), nil, forgedInfo)
	require.NoError(t, err)
	assert.Equal(t, "victim", ai.EnrollmentID())
}

func TestOwnerChainChecker_TrustedCache(t *testing.T) {
	f := newOwnerFixture(t)

	// untrusted certificates are never cached
	_, err := f.checker.check(f.forged.PEM)
	require.ErrorIs(t, err, identity.ErrUntrustedIdentity)
	assert.Equal(t, 0, f.checker.trusted.len())

	cert, err := f.checker.check(f.legit.PEM)
	require.NoError(t, err)
	assert.Equal(t, 1, f.checker.trusted.len())
	cached, err := f.checker.check(f.legit.PEM)
	require.NoError(t, err)
	assert.Same(t, cert, cached)

	// a cached certificate still goes through the audit info binding
	require.ErrorIs(t, f.checker.bind(f.legit.PEM, "mallory", nil), identity.ErrUntrustedIdentity)
}

func TestCertLRU_Bounded(t *testing.T) {
	l := newCertLRU(2)
	a, b, c := &x509.Certificate{}, &x509.Certificate{}, &x509.Certificate{}
	l.add("a", a)
	l.add("b", b)
	_, ok := l.get("a") // a becomes the most recent
	require.True(t, ok)
	l.add("c", c)
	assert.Equal(t, 2, l.len())
	_, ok = l.get("b")
	assert.False(t, ok, "least recently used entry must be evicted")
	got, ok := l.get("a")
	require.True(t, ok)
	assert.Same(t, a, got)
}

func TestOwnerChainChecker_ConcurrentCheck(t *testing.T) {
	f := newOwnerFixture(t)
	const n = 16
	legitErrs, forgedErrs := make([]error, n), make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, legitErrs[i] = f.checker.check(f.legit.PEM)
			_, forgedErrs[i] = f.checker.check(f.forged.PEM)
		})
	}
	wg.Wait()
	for i := range n {
		require.NoError(t, legitErrs[i])
		require.ErrorIs(t, forgedErrs[i], identity.ErrUntrustedIdentity)
	}
	assert.Equal(t, 1, f.checker.trusted.len())
}

// len returns the number of cached certificates.
func (l *certLRU) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.order.Len()
}

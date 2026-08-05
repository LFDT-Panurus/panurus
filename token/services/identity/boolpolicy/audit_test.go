/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package boolpolicy

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newX509Member returns a typed x509 member identity and its audit info bytes.
func newX509Member(t *testing.T, name, eid string) ([]byte, []byte) {
	t.Helper()
	member, err := identity.WrapWithType(x509.IdentityType, []byte(name))
	require.NoError(t, err)
	auditInfo, err := (&x509.AuditInfo{EID: eid, RH: []byte("rh-" + eid)}).Bytes()
	require.NoError(t, err)

	return member, auditInfo
}

// newPolicyIdentity wraps the members into a typed policy identity and the
// matching composite audit info blob.
func newPolicyIdentity(t *testing.T, policy string, members [][]byte, auditInfos [][]byte) (token.Identity, []byte) {
	t.Helper()
	inner, err := (&PolicyIdentity{Policy: policy, Identities: members}).Serialize()
	require.NoError(t, err)
	policyID, err := identity.WrapWithType(Policy, inner)
	require.NoError(t, err)
	wrapped, err := WrapAuditInfo(auditInfos)
	require.NoError(t, err)

	return policyID, wrapped
}

// newEIDRHDeserializer mirrors the driver wiring: x509 plus a recursive
// boolpolicy audit-info deserializer.
func newEIDRHDeserializer() *deserializer.EIDRHDeserializer {
	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{})
	d.AddDeserializer(Policy, NewAuditInfoDeserializer(d))

	return d
}

func TestPolicyEnrollmentIDCommonMembers(t *testing.T) {
	m0, ai0 := newX509Member(t, "cert-zero", "wallet-42")
	m1, ai1 := newX509Member(t, "cert-one", "wallet-42")
	policyID, wrapped := newPolicyIdentity(t, "$0 OR $1", [][]byte{m0, m1}, [][]byte{ai0, ai1})

	eid, rh, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.NoError(t, err)
	assert.Equal(t, "wallet-42", eid)
	assert.Empty(t, rh)
}

func TestPolicyEnrollmentIDSingleMember(t *testing.T) {
	m0, ai0 := newX509Member(t, "cert-zero", "wallet-7")
	policyID, wrapped := newPolicyIdentity(t, "$0", [][]byte{m0}, [][]byte{ai0})

	eid, _, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.NoError(t, err)
	assert.Equal(t, "wallet-7", eid)
}

func TestPolicyEnrollmentIDConflictingMembers(t *testing.T) {
	m0, ai0 := newX509Member(t, "cert-zero", "wallet-42")
	m1, ai1 := newX509Member(t, "cert-one", "wallet-43")
	policyID, wrapped := newPolicyIdentity(t, "$0 OR $1", [][]byte{m0, m1}, [][]byte{ai0, ai1})

	eid, _, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.NoError(t, err)
	assert.Empty(t, eid)
}

func TestPolicyEnrollmentIDEmptyMemberEID(t *testing.T) {
	// a member with no enrollment ID of its own: no common EID, not an error
	// (a nested composite spanning enrollments reports the same way)
	m0, ai0 := newX509Member(t, "cert-zero", "")
	policyID, wrapped := newPolicyIdentity(t, "$0", [][]byte{m0}, [][]byte{ai0})

	eid, _, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.NoError(t, err)
	assert.Empty(t, eid)
}

func TestPolicyEnrollmentIDNestedPolicy(t *testing.T) {
	// a member may itself be a policy identity, resolved recursively through
	// the parent multiplex deserializer
	cases := []struct {
		name     string
		innerEID [2]string
		want     string
	}{
		{"inner members share an enrollment", [2]string{"wallet-42", "wallet-42"}, "wallet-42"},
		{"inner members span enrollments", [2]string{"wallet-42", "wallet-43"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m0, ai0 := newX509Member(t, "cert-zero", tc.innerEID[0])
			m1, ai1 := newX509Member(t, "cert-one", tc.innerEID[1])
			innerID, innerInfo := newPolicyIdentity(t, "$0 OR $1", [][]byte{m0, m1}, [][]byte{ai0, ai1})
			outerID, wrapped := newPolicyIdentity(t, "$0", [][]byte{innerID}, [][]byte{innerInfo})

			eid, _, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), outerID, wrapped)
			require.NoError(t, err)
			assert.Equal(t, tc.want, eid)
		})
	}
}

func TestPolicyEnrollmentIDUnresolvableMember(t *testing.T) {
	// malformed: member typed with an identity type that has no registered
	// deserializer
	m0, err := identity.WrapWithType(identity.Type(99), []byte("cert-zero"))
	require.NoError(t, err)
	ai0, err := (&x509.AuditInfo{EID: "wallet-42"}).Bytes()
	require.NoError(t, err)
	policyID, wrapped := newPolicyIdentity(t, "$0", [][]byte{m0}, [][]byte{ai0})

	_, _, err = newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info of component")
}

func TestPolicyEnrollmentIDMemberCountMismatch(t *testing.T) {
	// malformed: two members but a single component audit info
	m0, ai0 := newX509Member(t, "cert-zero", "wallet-42")
	m1, _ := newX509Member(t, "cert-one", "wallet-42")
	policyID, wrapped := newPolicyIdentity(t, "$0 OR $1", [][]byte{m0, m1}, [][]byte{ai0})

	_, _, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "component audit infos")
}

func TestPolicyEnrollmentIDEmptyAuditInfoList(t *testing.T) {
	// malformed: one member but an empty audit info blob must still hit the
	// count check, not short-circuit to "no common EID"
	m0, _ := newX509Member(t, "cert-zero", "wallet-42")
	policyID, _ := newPolicyIdentity(t, "$0", [][]byte{m0}, [][]byte{[]byte("unused")})

	_, _, err := newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected 1 component audit infos but received 0")
}

func TestPolicyEnrollmentIDNoComponents(t *testing.T) {
	// malformed: a policy identity with no components at all
	inner, err := (&PolicyIdentity{Policy: "$0"}).Serialize()
	require.NoError(t, err)
	policyID, err := identity.WrapWithType(Policy, inner)
	require.NoError(t, err)

	_, _, err = newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no components")
}

func TestPolicyEnrollmentIDZeroValueDeserializer(t *testing.T) {
	// zero-value deserializer (no inner): legacy behavior, empty enrollment ID
	m0, ai0 := newX509Member(t, "cert-zero", "wallet-42")
	policyID, wrapped := newPolicyIdentity(t, "$0", [][]byte{m0}, [][]byte{ai0})

	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(Policy, &AuditInfoDeserializer{})

	eid, rh, err := d.GetEIDAndRH(t.Context(), policyID, wrapped)
	require.NoError(t, err)
	assert.Empty(t, eid)
	assert.Empty(t, rh)
}

func TestPolicyAuditInfoGarbageStillErrors(t *testing.T) {
	m0, _ := newX509Member(t, "cert-zero", "wallet-42")
	inner, err := (&PolicyIdentity{Policy: "$0", Identities: [][]byte{m0}}).Serialize()
	require.NoError(t, err)
	policyID, err := identity.WrapWithType(Policy, inner)
	require.NoError(t, err)

	_, _, err = newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, []byte("not-json"))
	require.Error(t, err)
}

func TestPolicyEnrollmentIDCrossEIDThenMalformedMember(t *testing.T) {
	// members legitimately span enrollments, but a later member is malformed:
	// the corruption must surface as an error, not be masked by the
	// cross-enrollment "" result
	unresolvable, err := identity.WrapWithType(identity.Type(99), []byte("cert-two"))
	require.NoError(t, err)
	unresolvableInfo, err := (&x509.AuditInfo{EID: "wallet-44"}).Bytes()
	require.NoError(t, err)

	m0, ai0 := newX509Member(t, "cert-zero", "wallet-42")
	m1, ai1 := newX509Member(t, "cert-one", "wallet-43")
	policyID, wrapped := newPolicyIdentity(t, "$0 OR $1 OR $2",
		[][]byte{m0, m1, unresolvable}, [][]byte{ai0, ai1, unresolvableInfo})

	_, _, err = newEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to deserialize audit info of component")
}

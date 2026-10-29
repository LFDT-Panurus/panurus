/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newX509Member returns a typed x509 member identity and its audit info bytes,
// built with the real x509.AuditInfo encoding. If eid is "garbage", the audit
// info is not a valid x509 audit info.
func newX509Member(t *testing.T, name, eid string) ([]byte, []byte) {
	t.Helper()
	member, err := identity.WrapWithType(x509.IdentityType, []byte(name))
	require.NoError(t, err)
	if eid == "garbage" {
		return member, []byte("not-an-x509-audit-info")
	}
	auditInfo, err := (&x509.AuditInfo{EID: eid, RH: []byte("rh-" + eid)}).Bytes()
	require.NoError(t, err)

	return member, auditInfo
}

// newPolicyIdentity wraps the members into a typed boolpolicy identity and the
// matching composite audit info.
func newPolicyIdentity(t *testing.T, policy string, members [][]byte, auditInfos [][]byte) (token.Identity, []byte) {
	t.Helper()
	inner, err := (&boolpolicy.PolicyIdentity{Policy: policy, Identities: members}).Serialize()
	require.NoError(t, err)
	policyID, err := identity.WrapWithType(boolpolicy.Policy, inner)
	require.NoError(t, err)
	wrapped, err := boolpolicy.WrapAuditInfo(auditInfos)
	require.NoError(t, err)

	return policyID, wrapped
}

// TestEIDRHDeserializer_PolicyEnrollmentID checks that the zkatsnark
// EIDRHDeserializer resolves the enrollment ID of a boolpolicy identity from
// its components, which requires the boolpolicy audit info deserializer to be
// wired with the inner (multiplex) deserializer, as zkatdlog and fabtoken do.
func TestEIDRHDeserializer_PolicyEnrollmentID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		eids    []string
		nilInfo []bool
		want    string
		wantErr string
	}{
		{name: "single member", eids: []string{"wallet-42"}, want: "wallet-42"},
		{name: "common members", eids: []string{"wallet-42", "wallet-42"}, want: "wallet-42"},
		{name: "conflicting members", eids: []string{"wallet-42", "wallet-43"}, want: ""},
		{name: "member audit info missing", eids: []string{"wallet-42"}, nilInfo: []bool{true}, want: ""},
		{name: "malformed member audit info", eids: []string{"wallet-42", "garbage"}, wantErr: "failed to deserialize audit info of component"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := make([][]byte, len(tc.eids))
			infos := make([][]byte, len(tc.eids))
			terms := make([]string, len(tc.eids))
			for i, eid := range tc.eids {
				members[i], infos[i] = newX509Member(t, "cert-"+strconv.Itoa(i), eid)
				if i < len(tc.nilInfo) && tc.nilInfo[i] {
					infos[i] = nil
				}
				terms[i] = "$" + strconv.Itoa(i)
			}
			policyID, wrapped := newPolicyIdentity(t, strings.Join(terms, " OR "), members, infos)

			eid, rh, err := zkatsnarkdriver.NewEIDRHDeserializer().GetEIDAndRH(t.Context(), policyID, wrapped)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, eid)
			assert.Empty(t, rh)
		})
	}
}

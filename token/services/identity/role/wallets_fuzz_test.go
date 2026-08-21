/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package role_test

import (
	"encoding/asn1"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role/mock"
	"github.com/stretchr/testify/require"
)

const maxFuzzRecipientBytes = 64 << 10

// FuzzLongTermOwnerWalletRegisterRecipientNoPanic hunts for recipient data
// that panics LongTermOwnerWallet.RegisterRecipient instead of returning an
// error. RegisterRecipient parses caller-supplied bytes — a typed-identity
// envelope, boolpolicy ASN.1 and audit-info JSON — so it must never panic,
// and it may accept only the wallet's own identity or a policy composite
// that lists the wallet's identity among its well-formed components with
// the wallet's own audit info at that component's entry.
func FuzzLongTermOwnerWalletRegisterRecipientNoPanic(f *testing.F) {
	owner := driver.Identity("ownerIdentity")
	ownerInfo := []byte("audit-info")
	policyID, err := boolpolicy.WrapPolicyIdentity("$0 OR $1",
		owner, token2.Identity("member2"))
	require.NoError(f, err)
	policyInfo, err := boolpolicy.WrapAuditInfo([][]byte{ownerInfo, []byte("ai2")})
	require.NoError(f, err)
	mismatchInfo, err := boolpolicy.WrapAuditInfo([][]byte{[]byte("ai1"), []byte("ai2")})
	require.NoError(f, err)
	garbageID, err := identity.WrapWithType(boolpolicy.Policy, identity.Identity("garbage"))
	require.NoError(f, err)
	foreignID, err := boolpolicy.WrapPolicyIdentity("$0 OR $1",
		token2.Identity("member1"), token2.Identity("member2"))
	require.NoError(f, err)
	trailingInner, err := (&boolpolicy.PolicyIdentity{
		Policy:     "$0 OR $1",
		Identities: [][]byte{owner, []byte("member2")},
	}).Serialize()
	require.NoError(f, err)
	trailingID, err := identity.WrapWithType(boolpolicy.Policy, append(trailingInner, 0x00))
	require.NoError(f, err)
	noPolicyInner, err := (&boolpolicy.PolicyIdentity{
		Identities: [][]byte{owner, []byte("member2")},
	}).Serialize()
	require.NoError(f, err)
	noPolicyID, err := identity.WrapWithType(boolpolicy.Policy, noPolicyInner)
	require.NoError(f, err)

	seeds := [][2][]byte{
		{owner, ownerInfo},                              // the wallet's own pair
		{owner, []byte("other-audit-info")},             // audit info mismatch
		{policyID, policyInfo},                          // accepted policy recipient
		{policyID, mismatchInfo},                        // foreign audit info at the owner's entry
		{foreignID, policyInfo},                         // historical gap: well-formed policy omitting the owner
		{trailingID, policyInfo},                        // valid DER followed by a trailing byte
		{noPolicyID, policyInfo},                        // empty policy expression
		{policyID, []byte(`{"IdentityAuditInfos":[]}`)}, // audit info count mismatch
		{policyID[:len(policyID)-1], policyInfo},        // truncated envelope
		{garbageID, policyInfo},                         // garbage under the policy tag
		{nil, nil},                                      // empty
		{[]byte("garbage"), []byte("garbage")},          // no envelope at all
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}

	f.Fuzz(func(t *testing.T, identityRaw, auditInfoRaw []byte) {
		if len(identityRaw) > maxFuzzRecipientBytes || len(auditInfoRaw) > maxFuzzRecipientBytes {
			t.Skip()
		}
		w, err := role.NewLongTermOwnerWallet(t.Context(),
			&mock.IdentityProvider{}, &mock.OwnerTokenVault{}, "w1",
			&mockIdentityInfo{id: string(owner)})
		require.NoError(t, err)
		require.NotPanics(t, func() {
			err := w.RegisterRecipient(t.Context(), &driver.RecipientData{
				Identity:  identityRaw,
				AuditInfo: auditInfoRaw,
			})
			if err != nil || owner.Equal(identityRaw) {
				return
			}
			// anything else accepted must satisfy every guard invariant of
			// the policy-recipient shape
			ti, tErr := identity.UnmarshalTypedIdentity(identityRaw)
			require.NoError(t, tErr)
			require.Equal(t, boolpolicy.Policy, ti.Type)
			pi := &boolpolicy.PolicyIdentity{}
			rest, piErr := asn1.Unmarshal(ti.Identity, pi)
			require.NoError(t, piErr)
			require.Empty(t, rest)
			require.NotEmpty(t, pi.Policy)
			require.NotEmpty(t, pi.Identities)
			require.LessOrEqual(t, len(pi.Identities), driver.DefaultResourceLimits().MaxIdentityComponents)
			memberIdx := -1
			seen := make(map[string]struct{}, len(pi.Identities))
			for k, component := range pi.Identities {
				require.NotEmpty(t, component)
				_, dup := seen[string(component)]
				require.False(t, dup)
				seen[string(component)] = struct{}{}
				if owner.Equal(component) {
					memberIdx = k
				}
			}
			require.GreaterOrEqual(t, memberIdx, 0)
			ai := &boolpolicy.AuditInfo{}
			require.NoError(t, json.Unmarshal(auditInfoRaw, ai))
			require.Len(t, ai.IdentityAuditInfos, len(pi.Identities))
			require.Equal(t, ownerInfo, ai.IdentityAuditInfos[memberIdx].AuditInfo)
		})
	})
}

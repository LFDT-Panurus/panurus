/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multisig

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMemberAuditInfo is a member audit info carrying a fixed enrollment ID.
type fakeMemberAuditInfo struct{ eid string }

func (f *fakeMemberAuditInfo) EnrollmentID() string     { return f.eid }
func (f *fakeMemberAuditInfo) RevocationHandle() string { return "" }

// fakeInner resolves a member audit info blob to the enrollment ID it spells,
// returns nil audit info for "nil", and fails for "error".
type fakeInner struct{ calls int }

func (f *fakeInner) DeserializeAuditInfo(_ context.Context, _ driver.Identity, raw []byte) (driver2.AuditInfo, error) {
	f.calls++
	switch string(raw) {
	case "nil":
		return nil, nil
	case "error":
		return nil, errors.New("inner failure")
	}

	return &fakeMemberAuditInfo{eid: string(raw)}, nil
}

// multisigFixture returns the inner MultiIdentity bytes for n members and the
// multisig audit info wrapping memberInfos.
func multisigFixture(t *testing.T, n int, memberInfos ...[]byte) ([]byte, []byte) {
	t.Helper()
	ids := make([]token.Identity, n)
	for i := range ids {
		ids[i] = []byte{byte('a' + i)}
	}
	rawID, err := (&MultiIdentity{Identities: ids}).Bytes()
	require.NoError(t, err)
	ai := &AuditInfo{IdentityAuditInfos: make([]IdentityAuditInfo, len(memberInfos))}
	for k, info := range memberInfos {
		ai.IdentityAuditInfos[k].AuditInfo = info
	}
	rawInfo, err := ai.Bytes()
	require.NoError(t, err)

	return rawID, rawInfo
}

func TestAuditInfoDeserializer_CommonEnrollmentID(t *testing.T) {
	tests := []struct {
		name    string
		members int
		infos   [][]byte
		noInner bool
		ctx     func(context.Context) context.Context
		wantEID string
		wantErr string
	}{
		{name: "all members alice", members: 2, infos: [][]byte{[]byte("alice"), []byte("alice")}, wantEID: "alice"},
		{name: "single member", members: 1, infos: [][]byte{[]byte("alice")}, wantEID: "alice"},
		{name: "alice and bob", members: 2, infos: [][]byte{[]byte("alice"), []byte("bob")}},
		{name: "missing member audit info", members: 2, infos: [][]byte{[]byte("alice"), nil}},
		{name: "inner returns nil", members: 2, infos: [][]byte{[]byte("alice"), []byte("nil")}},
		{name: "member without eid", members: 2, infos: [][]byte{[]byte(""), []byte("")}},
		{name: "nil inner", members: 2, infos: [][]byte{[]byte("alice"), []byte("alice")}, noInner: true},
		{name: "count mismatch", members: 2, infos: [][]byte{[]byte("alice")}, wantErr: "expected 2 member audit infos but received 1"},
		{name: "zero members", members: 0, infos: nil, wantErr: "multisig identity has no members"},
		{name: "inner error", members: 2, infos: [][]byte{[]byte("alice"), []byte("error")}, wantErr: "inner failure"},
		{
			name: "depth limit", members: 1, infos: [][]byte{[]byte("alice")},
			ctx: func(ctx context.Context) context.Context {
				// one level allowed and already consumed by an enclosing composite
				ctx, err := driver.EnterCompositeIdentity(driver.WithIdentityNestingLimits(ctx, 1, 8))
				require.NoError(t, err)

				return ctx
			},
			wantErr: "cannot derive the enrollment ID of a multisig identity",
		},
		{
			name: "too many members", members: 3, infos: [][]byte{[]byte("alice"), []byte("alice"), []byte("alice")},
			ctx:     func(ctx context.Context) context.Context { return driver.WithIdentityNestingLimits(ctx, 4, 2) },
			wantErr: "invalid multisig identity",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rawID, rawInfo := multisigFixture(t, tc.members, tc.infos...)
			d := NewAuditInfoDeserializer(&fakeInner{})
			if tc.noInner {
				d = NewAuditInfoDeserializer(nil)
			}
			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}
			ai, err := d.DeserializeAuditInfo(ctx, rawID, rawInfo)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantEID, ai.EnrollmentID())
			assert.Empty(t, ai.RevocationHandle())
		})
	}
}

// TestAuditInfoDeserializer_EIDNotSerialized pins that the derived enrollment
// ID does not leak into the wire format of the audit info.
func TestAuditInfoDeserializer_EIDNotSerialized(t *testing.T) {
	rawID, rawInfo := multisigFixture(t, 2, []byte("alice"), []byte("alice"))
	ai, err := NewAuditInfoDeserializer(&fakeInner{}).DeserializeAuditInfo(t.Context(), rawID, rawInfo)
	require.NoError(t, err)
	require.Equal(t, "alice", ai.EnrollmentID())
	raw, err := ai.(*AuditInfo).Bytes()
	require.NoError(t, err)
	assert.Equal(t, rawInfo, raw)
}

// newX509Member returns a typed x509 member identity and its audit info bytes.
func newX509Member(t *testing.T, name, eid string) ([]byte, []byte) {
	t.Helper()
	member, err := identity.WrapWithType(x509.IdentityType, []byte(name))
	require.NoError(t, err)
	auditInfo, err := (&x509.AuditInfo{EID: eid, RH: []byte("rh-" + eid)}).Bytes()
	require.NoError(t, err)

	return member, auditInfo
}

// TestEIDRHDeserializer_Multisig runs the driver wiring: an EIDRHDeserializer
// with x509 and a recursive multisig audit-info deserializer.
func TestEIDRHDeserializer_Multisig(t *testing.T) {
	d := deserializer.NewEIDRHDeserializer()
	d.AddDeserializer(x509.IdentityType, &x509.AuditInfoDeserializer{})
	d.AddDeserializer(Multisig, NewAuditInfoDeserializer(d))

	m0, ai0 := newX509Member(t, "cert-zero", "alice")
	m1, ai1 := newX509Member(t, "cert-one", "alice")
	m2, ai2 := newX509Member(t, "cert-two", "bob")

	shared, err := WrapIdentities(m0, m1)
	require.NoError(t, err)
	sharedInfo, err := WrapAuditInfo([][]byte{ai0, ai1})
	require.NoError(t, err)
	eid, rh, err := d.GetEIDAndRH(t.Context(), shared, sharedInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", eid)
	assert.Empty(t, rh)

	spanning, err := WrapIdentities(m0, m2)
	require.NoError(t, err)
	spanningInfo, err := WrapAuditInfo([][]byte{ai0, ai2})
	require.NoError(t, err)
	eid, _, err = d.GetEIDAndRH(t.Context(), spanning, spanningInfo)
	require.NoError(t, err)
	assert.Empty(t, eid)

	// nested: a multisig of the shared multisig and a third alice member
	m3, ai3 := newX509Member(t, "cert-three", "alice")
	nested, err := WrapIdentities(shared, m3)
	require.NoError(t, err)
	nestedInfo, err := WrapAuditInfo([][]byte{sharedInfo, ai3})
	require.NoError(t, err)
	eid, _, err = d.GetEIDAndRH(t.Context(), nested, nestedInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", eid)
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
)

// fakeEnrollment is a minimal enrollmentResolver whose two lookups are preset per test case, and
// which counts its calls so the short-circuit (no EID lookup without audit info) can be asserted.
type fakeEnrollment struct {
	auditInfo  []byte
	auditErr   error
	eid        string
	rh         string
	eidErr     error
	auditCalls int
	eidCalls   int
}

func (f *fakeEnrollment) GetAuditInfo(context.Context, driver.Identity) ([]byte, error) {
	f.auditCalls++

	return f.auditInfo, f.auditErr
}

func (f *fakeEnrollment) GetEIDAndRH(context.Context, driver.Identity, []byte) (string, string, error) {
	f.eidCalls++

	return f.eid, f.rh, f.eidErr
}

// TestEnrollmentKeyResolver pins the resolver's contract: a namespaced enrollment-ID key for an
// identity with locally known audit info, and "" (fall back to the identity hash) for every case
// it cannot resolve cheaply — no audit info, a lookup error, or an empty enrollment ID — without
// chasing the enrollment ID when there is no audit info to derive it from.
func TestEnrollmentKeyResolver(t *testing.T) {
	tests := []struct {
		name          string
		fake          fakeEnrollment
		wantKey       string
		wantEIDLookup bool
	}{
		{
			name:          "resolves to a namespaced enrollment id",
			fake:          fakeEnrollment{auditInfo: []byte("audit-info"), eid: "alice"},
			wantKey:       "eid:alice",
			wantEIDLookup: true,
		},
		{
			name:          "no audit info falls back and never looks up the eid",
			fake:          fakeEnrollment{auditInfo: nil},
			wantKey:       "",
			wantEIDLookup: false,
		},
		{
			name:          "audit-info error falls back and never looks up the eid",
			fake:          fakeEnrollment{auditErr: errors.New("boom")},
			wantKey:       "",
			wantEIDLookup: false,
		},
		{
			name:          "empty enrollment id falls back",
			fake:          fakeEnrollment{auditInfo: []byte("audit-info"), eid: ""},
			wantKey:       "",
			wantEIDLookup: true,
		},
		{
			name:          "enrollment-id error falls back",
			fake:          fakeEnrollment{auditInfo: []byte("audit-info"), eidErr: errors.New("boom")},
			wantKey:       "",
			wantEIDLookup: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := test.fake
			r := newEnrollmentKeyResolver(&fake)

			key := r.ThrottleKey(t.Context(), Identity("a_pseudonym"))

			assert.Equal(t, test.wantKey, key)
			assert.Equal(t, 1, fake.auditCalls, "audit info must be looked up exactly once")
			if test.wantEIDLookup {
				assert.Equal(t, 1, fake.eidCalls, "the enrollment id must be derived from the audit info")
			} else {
				assert.Zero(t, fake.eidCalls, "the enrollment id must not be chased without audit info")
			}
		})
	}
}

// TestEnrollmentKeyResolverToleratesNilProvider guards the defensive nil checks: a resolver with no
// provider resolves nothing rather than panicking on the signature path.
func TestEnrollmentKeyResolverToleratesNilProvider(t *testing.T) {
	assert.Empty(t, newEnrollmentKeyResolver(nil).ThrottleKey(t.Context(), Identity("x")))

	var r *enrollmentKeyResolver
	assert.Empty(t, r.ThrottleKey(t.Context(), Identity("x")))
}

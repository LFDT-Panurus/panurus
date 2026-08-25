/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wallet_test

import (
	"bytes"
	"testing"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/stretchr/testify/require"
)

// maxFuzzRecipientBytes bounds generated inputs. It sits just above
// MaxAuditInfoLength so both sides of that bound stay reachable while keeping each
// execution cheap.
const maxFuzzRecipientBytes = wallet.MaxAuditInfoLength + 1024

// FuzzRegisterRecipientIdentityStructuralGuard hunts for recipient data that panics
// RegisterRecipientIdentity instead of returning an error, and for drift between the
// structural guard's advertised bounds and what it actually rejects.
//
// RegisterRecipientIdentity is the entry point through which a counterparty's own
// identity and audit-info bytes reach the node, so it must never panic on them. The
// service is built with an IdentityProvider and a Deserializer that always succeed,
// which makes the guard the only source of errors: the call must fail exactly when
// the identity length is outside [MinIdentityLength, MaxIdentityLength] or the audit
// info is empty or longer than MaxAuditInfoLength, and must succeed otherwise.
//
// Generated inputs are capped at maxFuzzRecipientBytes so the campaign is not spent
// mutating multi-megabyte blobs; the MaxIdentityLength bound itself (1 MiB) is covered
// as an ordinary case by TestValidateBasicStructure.
func FuzzRegisterRecipientIdentityStructuralGuard(f *testing.F) {
	minIdentity := bytes.Repeat([]byte("i"), wallet.MinIdentityLength)
	shortIdentity := bytes.Repeat([]byte("i"), wallet.MinIdentityLength-1)
	maxAuditInfo := bytes.Repeat([]byte("a"), wallet.MaxAuditInfoLength)
	largeAuditInfo := bytes.Repeat([]byte("a"), wallet.MaxAuditInfoLength+1)

	seeds := [][2][]byte{
		{[]byte("identity-long-enough"), []byte(`{"key":"value"}`)}, // accepted pair
		{nil, nil},                                                       // empty
		{[]byte{}, []byte("audit-info")},                                 // empty identity
		{[]byte("identity-long-enough"), []byte{}},                       // empty audit info
		{minIdentity, []byte("a")},                                       // identity at the lower bound
		{shortIdentity, []byte("audit-info")},                            // identity one byte too short
		{[]byte("identity-long-enough"), maxAuditInfo},                   // audit info at the upper bound
		{[]byte("identity-long-enough"), largeAuditInfo},                 // audit info one byte too large
		{[]byte{0x00, 0x00, 0x00}, []byte{0xff, 0xfe}},                   // short, non-UTF8 bytes
		{bytes.Repeat([]byte{0x00}, 32), bytes.Repeat([]byte{0x00}, 32)}, // all-zero bytes
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}

	f.Fuzz(func(t *testing.T, identityRaw, auditInfoRaw []byte) {
		if len(identityRaw) > maxFuzzRecipientBytes || len(auditInfoRaw) > maxFuzzRecipientBytes {
			t.Skip()
		}
		service, _, _ := newService()

		var err error
		require.NotPanics(t, func() {
			err = service.RegisterRecipientIdentity(t.Context(), &tdriver.RecipientData{
				Identity:  identityRaw,
				AuditInfo: auditInfoRaw,
			})
		})

		outOfBounds := len(identityRaw) < wallet.MinIdentityLength ||
			len(identityRaw) > wallet.MaxIdentityLength ||
			len(auditInfoRaw) == 0 ||
			len(auditInfoRaw) > wallet.MaxAuditInfoLength
		if outOfBounds {
			require.Errorf(t, err, "guard accepted out-of-bounds data: identity %d bytes, audit info %d bytes",
				len(identityRaw), len(auditInfoRaw))

			return
		}
		require.NoErrorf(t, err, "guard rejected in-bounds data: identity %d bytes, audit info %d bytes",
			len(identityRaw), len(auditInfoRaw))
	})
}

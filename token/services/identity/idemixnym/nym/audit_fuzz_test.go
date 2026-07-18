/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"encoding/json"
	"testing"

	idemix "github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/stretchr/testify/require"
)

const maxFuzzNymAuditInfoBytes = 64 << 10

// FuzzDeserializeAuditInfoNoPanic hunts for malformed JSON that panics
// DeserializeAuditInfo instead of returning an error. This is the
// deserialization entry point for attacker-controlled signer info bytes,
// reached from km.go's signerInfo and skiprovider.go's GetSKIsFromIdentity
// (see Finding 8: the embedded crypto.AuditInfo's EnrollmentID/
// RevocationHandle unconditionally indexed into Attributes, and Match
// dereferenced EidNymAuditData/RhNymAuditData without nil checks).
func FuzzDeserializeAuditInfoNoPanic(f *testing.F) {
	valid := &AuditInfo{
		AuditInfo: &crypto.AuditInfo{
			Attributes: [][]byte{
				[]byte("attr0"),
				[]byte("attr1"),
				[]byte("enrollment-id"),
				[]byte("revocation-handle"),
			},
			Schema:          "test-schema",
			EidNymAuditData: &idemix.AttrNymAuditData{},
			RhNymAuditData:  &idemix.AttrNymAuditData{},
		},
		IdemixSignature: []byte("signature"),
	}
	validBytes, err := json.Marshal(valid)
	require.NoError(f, err)
	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte("invalid json"))
	f.Add([]byte("{}"))
	f.Add([]byte(`{"IdemixSignature":"c2ln"}`))
	f.Add([]byte(`{"AuditInfo":{"Attributes":[[0]]},"IdemixSignature":"c2ln"}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzNymAuditInfoBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = DeserializeAuditInfo(raw)
		})
	})
}

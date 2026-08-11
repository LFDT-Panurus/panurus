/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"testing"

	idemix "github.com/IBM/idemix/bccsp/types"
	"github.com/stretchr/testify/require"
)

const maxFuzzAuditInfoBytes = 64 << 10

// FuzzDeserializeAuditInfoNoPanic hunts for malformed JSON that panics
// DeserializeAuditInfo instead of returning an error. This is the
// deserialization entry point for attacker-controlled audit info bytes,
// reached from km.go's Info and from idemixnym's own AuditInfo.Validate
// delegation (see Finding 8: EnrollmentID/RevocationHandle unconditionally
// indexed into Attributes, and Match dereferenced EidNymAuditData/
// RhNymAuditData without nil checks).
func FuzzDeserializeAuditInfoNoPanic(f *testing.F) {
	valid := &AuditInfo{
		Attributes: [][]byte{
			[]byte("attr0"),
			[]byte("attr1"),
			[]byte("enrollment-id"),
			[]byte("revocation-handle"),
		},
		Schema:          "test-schema",
		EidNymAuditData: &idemix.AttrNymAuditData{},
		RhNymAuditData:  &idemix.AttrNymAuditData{},
	}
	validBytes, err := valid.Bytes()
	require.NoError(f, err)
	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte("invalid json"))
	f.Add([]byte("{}"))
	f.Add([]byte(`{"Attributes":[[0],[1],[101,105,100],[114,104]],"Schema":""}`))
	f.Add([]byte(`{"Attributes":[[0]]}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzAuditInfoBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = DeserializeAuditInfo(raw)
		})
	})
}

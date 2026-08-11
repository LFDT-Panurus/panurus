/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"strconv"
	"testing"

	idemix "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
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
	attributes := [][]byte{
		[]byte("attr0"),
		[]byte("attr1"),
		[]byte("enrollment-id"),
		[]byte("revocation-handle"),
	}
	valid := &AuditInfo{
		Attributes:      attributes,
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

	// The seeds above leave the pseudonym audit data zero-valued, which marshals
	// its mathlib fields as JSON null and so never reaches their UnmarshalJSON.
	// Seed populated curve elements too, along with a curve ID no curve is
	// registered under: mathlib indexes math.Curves with that ID unchecked, so
	// this is the input class that used to panic instead of being rejected.
	curve := math.Curves[math.BLS12_381]
	populated := &AuditInfo{
		Attributes: attributes,
		Schema:     "test-schema",
		EidNymAuditData: &idemix.AttrNymAuditData{
			Nym:  curve.GenG1,
			Rand: curve.NewZrFromInt(7),
			Attr: curve.NewZrFromInt(11),
		},
		RhNymAuditData: &idemix.AttrNymAuditData{
			Nym:  curve.GenG1,
			Rand: curve.NewZrFromInt(13),
			Attr: curve.NewZrFromInt(17),
		},
	}
	populatedBytes, err := populated.Bytes()
	require.NoError(f, err)
	f.Add(populatedBytes)
	f.Add([]byte(`{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}}}`))
	f.Add([]byte(`{"EidNymAuditData":{"Rand":{"curve":-1,"element":"AQID"}}}`))
	f.Add([]byte(`{"RhNymAuditData":{"Attr":{"curve":` + strconv.Itoa(len(math.Curves)) + `,"element":"AQID"}}}`))
	// Out-of-range curve ids that a pre-validating version of the guard failed to
	// see, because encoding/json reaches them but they do not survive into the
	// decoded result. See TestDeserializeAuditInfoOutOfRangeCurveIDEvasions.
	f.Add([]byte(`{"EidNymAuditData":5,"RhNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}}}`))
	f.Add([]byte(`{"RhNymAuditData":{"Attr":{"curve":9}}}0`))
	f.Add([]byte(`{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"},"Nym":null}}`))
	f.Add([]byte(`{"eidnymauditdata":{"nym":{"CURVE":999999,"element":"AQID"}}}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzAuditInfoBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = DeserializeAuditInfo(raw)
		})
	})
}

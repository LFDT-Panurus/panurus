/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"encoding/json"
	"strconv"
	"testing"

	idemix "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
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
	attributes := [][]byte{
		[]byte("attr0"),
		[]byte("attr1"),
		[]byte("enrollment-id"),
		[]byte("revocation-handle"),
	}
	valid := &AuditInfo{
		AuditInfo: &crypto.AuditInfo{
			Attributes:      attributes,
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

	// The seeds above leave the pseudonym audit data zero-valued, which marshals
	// its mathlib fields as JSON null and so never reaches their UnmarshalJSON.
	// Seed populated curve elements too, along with a curve ID no curve is
	// registered under: mathlib indexes math.Curves with that ID unchecked, so
	// this is the input class that used to panic instead of being rejected.
	curve := math.Curves[math.BLS12_381]
	populated := &AuditInfo{
		AuditInfo: &crypto.AuditInfo{
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
		},
		IdemixSignature: []byte("signature"),
	}
	populatedBytes, err := json.Marshal(populated)
	require.NoError(f, err)
	f.Add(populatedBytes)
	f.Add([]byte(`{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}},"IdemixSignature":"c2ln"}`))
	f.Add([]byte(`{"EidNymAuditData":{"Rand":{"curve":-1,"element":"AQID"}},"IdemixSignature":"c2ln"}`))
	f.Add([]byte(`{"RhNymAuditData":{"Attr":{"curve":` + strconv.Itoa(len(math.Curves)) + `,"element":"AQID"}},"IdemixSignature":"c2ln"}`))
	// Out-of-range curve ids that a pre-validating version of the guard failed to
	// see, because encoding/json reaches them but they do not survive into the
	// decoded result. See TestDeserializeAuditInfoOutOfRangeCurveIDEvasions.
	f.Add([]byte(`{"EidNymAuditData":5,"RhNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}},"IdemixSignature":"c2ln"}`))
	f.Add([]byte(`{"RhNymAuditData":{"Attr":{"curve":9}},"IdemixSignature":"c2ln"}0`))
	f.Add([]byte(`{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"},"Nym":null},"IdemixSignature":"c2ln"}`))
	f.Add([]byte(`{"eidnymauditdata":{"nym":{"CURVE":999999,"element":"AQID"}},"idemixsignature":"c2ln"}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzNymAuditInfoBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_, _ = DeserializeAuditInfo(raw)
		})
	})
}

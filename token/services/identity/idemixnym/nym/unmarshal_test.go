/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"encoding/json"
	"strconv"
	"testing"

	csp "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeserializeAuditInfoOutOfRangeCurveID covers the curve IDs no curve is
// registered under. AuditInfo embeds *crypto.AuditInfo, so its pseudonym audit
// data is inlined at the top level of this JSON and this decode reaches the
// panicking mathlib elements itself; before the guard, each of these payloads
// killed the process with an "index out of range" panic.
func TestDeserializeAuditInfoOutOfRangeCurveID(t *testing.T) {
	for _, curveID := range []int{-1, len(math.Curves), 999999} {
		for _, field := range []string{"Nym", "Rand", "Attr"} {
			t.Run(field+"/"+strconv.Itoa(curveID), func(t *testing.T) {
				raw := []byte(`{"EidNymAuditData":{"` + field + `":{"curve":` +
					strconv.Itoa(curveID) + `,"element":"AQID"}},"IdemixSignature":"c2ln"}`)

				var (
					result *AuditInfo
					err    error
				)
				require.NotPanics(t, func() {
					result, err = DeserializeAuditInfo(raw)
				})
				require.Error(t, err)
				assert.Nil(t, result)

				require.NotPanics(t, func() {
					err = (&AuditInfo{}).FromBytes(raw)
				})
				require.Error(t, err)
			})
		}
	}
}

// TestDeserializeAuditInfoOutOfRangeCurveIDEvasions mirrors the crypto package's
// test of the same name: payloads that hid an out-of-range curve ID from an earlier
// version of this guard, which pre-validated the raw bytes with its own decode pass
// instead of wrapping the real one.
func TestDeserializeAuditInfoOutOfRangeCurveIDEvasions(t *testing.T) {
	for name, raw := range map[string]string{
		"trailing garbage": `{"RhNymAuditData":{"Attr":{"curve":9}},"IdemixSignature":"c2ln"}0`,
		"duplicate outer key": `{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}},` +
			`"EidNymAuditData":{"Nym":null},"IdemixSignature":"c2ln"}`,
		"duplicate inner key": `{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"},` +
			`"Nym":null},"IdemixSignature":"c2ln"}`,
		"type error first": `{"EidNymAuditData":5,"RhNymAuditData":` +
			`{"Nym":{"curve":999999,"element":"AQID"}},"IdemixSignature":"c2ln"}`,
		"unexpected key casing": `{"eidnymauditdata":{"nym":{"CURVE":999999,"element":"AQID"}},` +
			`"idemixsignature":"c2ln"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = DeserializeAuditInfo([]byte(raw))
			})
			require.Error(t, err)
		})
	}
}

// TestDeserializeAuditInfoPopulatedCurveElementsRoundTrip makes sure the guard does
// not get in the way of audit info that actually carries curve elements.
func TestDeserializeAuditInfoPopulatedCurveElementsRoundTrip(t *testing.T) {
	for curveID := range math.Curves {
		t.Run(strconv.Itoa(curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			auditData := func() *csp.AttrNymAuditData {
				return &csp.AttrNymAuditData{
					Nym:  curve.GenG1,
					Rand: curve.NewZrFromInt(7),
					Attr: curve.NewZrFromInt(11),
				}
			}
			auditInfo := &AuditInfo{
				AuditInfo: &crypto.AuditInfo{
					Attributes: [][]byte{
						[]byte("attr0"),
						[]byte("attr1"),
						[]byte("enrollment-id"),
						[]byte("revocation-handle"),
					},
					Schema:          "test-schema",
					EidNymAuditData: auditData(),
					RhNymAuditData:  auditData(),
				},
				IdemixSignature: []byte("signature"),
			}
			raw, err := json.Marshal(auditInfo)
			require.NoError(t, err)

			deserialized, err := DeserializeAuditInfo(raw)
			require.NoError(t, err)
			assert.Equal(t, auditInfo.IdemixSignature, deserialized.IdemixSignature)
			assert.Equal(t, auditInfo.EnrollmentID(), deserialized.EnrollmentID())
			assert.True(t, auditInfo.EidNymAuditData.Nym.Equals(deserialized.EidNymAuditData.Nym))
			assert.True(t, auditInfo.RhNymAuditData.Rand.Equals(deserialized.RhNymAuditData.Rand))
		})
	}
}

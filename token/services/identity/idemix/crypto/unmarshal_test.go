/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"strconv"
	"testing"

	csp "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditInfoWithCurveID returns audit info JSON whose EID pseudonym audit data
// carries the given curve ID in the named field. The curve ID goes in verbatim,
// so it can be a value no curve is registered under.
func auditInfoWithCurveID(field string, curveID int) []byte {
	return []byte(`{"EidNymAuditData":{"` + field + `":{"curve":` +
		strconv.Itoa(curveID) + `,"element":"AQID"}},"Schema":"test-schema"}`)
}

// TestDeserializeAuditInfoOutOfRangeCurveID covers the curve IDs no curve is
// registered under. mathlib uses each of them to index its curve table, so before
// the guard every one of these payloads killed the process with an
// "index out of range" panic rather than being rejected.
func TestDeserializeAuditInfoOutOfRangeCurveID(t *testing.T) {
	for _, curveID := range []int{-1, len(math.Curves), len(math.Curves) + 1, 999999} {
		for _, field := range []string{"Nym", "Rand", "Attr"} {
			t.Run(field+"/"+strconv.Itoa(curveID), func(t *testing.T) {
				raw := auditInfoWithCurveID(field, curveID)

				var err error
				require.NotPanics(t, func() {
					_, err = DeserializeAuditInfo(raw)
				})
				require.Error(t, err)

				require.NotPanics(t, func() {
					err = (&AuditInfo{}).FromBytes(raw)
				})
				require.Error(t, err)
			})
		}
	}
}

// TestDeserializeAuditInfoOutOfRangeCurveIDEvasions collects payloads that hid an
// out-of-range curve ID from an earlier version of this guard, which pre-validated
// the raw bytes with its own decode pass instead of wrapping the real one. Each
// exploits a way that pass diverged from what encoding/json actually traverses,
// and each panicked while the pre-validation reported the payload as clean.
func TestDeserializeAuditInfoOutOfRangeCurveIDEvasions(t *testing.T) {
	for name, raw := range map[string]string{
		// json.Unmarshal validates the whole input up front and decodes nothing on
		// a syntax error, but FromBytes' decoder reads one value and ignores what
		// follows it. Found by FuzzDeserializeAuditInfoNoPanic.
		"trailing garbage": `{"RhNymAuditData":{"Attr":{"curve":9}}}0`,
		// mathlib's UnmarshalJSON runs on every occurrence of a key, so the first
		// one panics even though only the last survives in the decoded result.
		"duplicate outer key": `{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}},` +
			`"EidNymAuditData":{"Nym":null}}`,
		"duplicate inner key": `{"EidNymAuditData":{"Nym":{"curve":999999,"element":"AQID"},"Nym":null}}`,
		// encoding/json records a type error and keeps decoding, so the curve
		// element after it is still reached.
		"type error first": `{"EidNymAuditData":5,"RhNymAuditData":{"Nym":{"curve":999999,"element":"AQID"}}}`,
		// Key matching is case-insensitive.
		"unexpected key casing": `{"eidnymauditdata":{"nym":{"CURVE":999999,"element":"AQID"}}}`,
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

// TestUnmarshalAuditInfo covers the guard on its own: a decode that panics becomes
// an error naming the panic, and anything else is passed through untouched.
func TestUnmarshalAuditInfo(t *testing.T) {
	err := UnmarshalAuditInfo(func() error {
		panic("boom")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "caught panic")
	assert.Contains(t, err.Error(), "boom")

	sentinel := errors.New("decode failed")
	assert.Equal(t, sentinel, UnmarshalAuditInfo(func() error { return sentinel }))
	require.NoError(t, UnmarshalAuditInfo(func() error { return nil }))
}

// TestAuditInfoPopulatedCurveElementsRoundTrip makes sure the guard does not get
// in the way of audit info that actually carries curve elements — the case the
// fuzz seed corpus used to miss, since a zero-value AttrNymAuditData marshals its
// mathlib fields as JSON null and never reaches their UnmarshalJSON.
func TestAuditInfoPopulatedCurveElementsRoundTrip(t *testing.T) {
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
				Attributes: [][]byte{
					[]byte("attr0"),
					[]byte("attr1"),
					[]byte("enrollment-id"),
					[]byte("revocation-handle"),
				},
				Schema:          "test-schema",
				EidNymAuditData: auditData(),
				RhNymAuditData:  auditData(),
			}
			raw, err := auditInfo.Bytes()
			require.NoError(t, err)

			deserialized, err := DeserializeAuditInfo(raw)
			require.NoError(t, err)
			assert.Equal(t, auditInfo.EnrollmentID(), deserialized.EnrollmentID())
			assert.Equal(t, auditInfo.RevocationHandle(), deserialized.RevocationHandle())
			assert.True(t, auditInfo.EidNymAuditData.Nym.Equals(deserialized.EidNymAuditData.Nym))
			assert.True(t, auditInfo.EidNymAuditData.Rand.Equals(deserialized.EidNymAuditData.Rand))
			assert.True(t, auditInfo.RhNymAuditData.Attr.Equals(deserialized.RhNymAuditData.Attr))
		})
	}
}

// TestDeserializeAuditInfoInRangeCurveIDAccepted checks the guard does not turn
// every curve ID into an error: the boundary IDs a curve is registered under are
// still decoded.
func TestDeserializeAuditInfoInRangeCurveIDAccepted(t *testing.T) {
	for _, curveID := range []int{0, len(math.Curves) - 1} {
		t.Run(strconv.Itoa(curveID), func(t *testing.T) {
			curve := math.Curves[curveID]
			raw, err := (&AuditInfo{
				Attributes: [][]byte{{0}, {1}, []byte("eid"), []byte("rh")},
				EidNymAuditData: &csp.AttrNymAuditData{
					Nym: curve.GenG1, Rand: curve.NewZrFromInt(1), Attr: curve.NewZrFromInt(2),
				},
				RhNymAuditData: &csp.AttrNymAuditData{
					Nym: curve.GenG1, Rand: curve.NewZrFromInt(3), Attr: curve.NewZrFromInt(4),
				},
			}).Bytes()
			require.NoError(t, err)

			ai, err := DeserializeAuditInfo(raw)
			require.NoError(t, err)
			assert.Equal(t, curveID, int(ai.EidNymAuditData.Nym.CurveID()))
		})
	}
}

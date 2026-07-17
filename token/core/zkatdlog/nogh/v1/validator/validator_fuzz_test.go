/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	fv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzActionBytes = 256 << 10

// fuzzCurve is a fixed, non-nil curve used to build well-formed G1/Zr elements for the
// seed corpus. Any curve works here: these seeds only need to survive Serialize/Deserialize,
// not a real ZK verification.
var fuzzCurve = math.Curves[math.BLS12_381_BBS_GURVY]

// validIssueRaw returns the serialized bytes of a structurally complete, non-anonymous
// issue action. It is not cryptographically valid (the proof is a placeholder), but it
// exercises every field the deserializer touches.
func validIssueRaw(tb testing.TB, proofType rp.ProofType) []byte {
	tb.Helper()
	raw, err := (&issue.Action{
		Issuer: []byte("issuer"),
		Outputs: []*token.Token{
			{Owner: []byte("owner-1"), Data: fuzzCurve.GenG1},
			{Owner: []byte("owner-2"), Data: fuzzCurve.GenG1},
		},
		ProofType: proofType,
		Proof:     []byte("proof"),
		Metadata:  map[string][]byte{"key": []byte("value")},
	}).Serialize()
	require.NoError(tb, err)

	return raw
}

// validTransferRaw returns the serialized bytes of a structurally complete transfer
// action. When redeem is true the sole output has no owner and an issuer is attached,
// mirroring a redeem transfer.
func validTransferRaw(tb testing.TB, proofType rp.ProofType, redeem bool) []byte {
	tb.Helper()
	action := &transfer.Action{
		Inputs: []*transfer.ActionInput{
			{
				ID:    &token2.ID{TxId: "tx0", Index: 0},
				Token: &token.Token{Owner: []byte("owner-1"), Data: fuzzCurve.GenG1},
				UpgradeWitness: &token.UpgradeWitness{
					FabToken:       &fv1.Output{Owner: []byte("owner-1"), Type: "ABC", Quantity: "0x10"},
					BlindingFactor: fuzzCurve.NewZrFromInt(1),
				},
			},
		},
		ProofType: proofType,
		Proof:     []byte("proof"),
		Metadata:  map[string][]byte{"key": []byte("value")},
	}
	if redeem {
		action.Issuer = []byte("issuer")
		action.Outputs = []*token.Token{{Owner: nil, Data: fuzzCurve.GenG1}}
	} else {
		action.Outputs = []*token.Token{{Owner: []byte("owner-2"), Data: fuzzCurve.GenG1}}
	}
	raw, err := action.Serialize()
	require.NoError(tb, err)

	return raw
}

func actionTypeFor(kind uint8) request.ActionType {
	if kind%2 == 1 {
		return request.ActionType_ACTION_TYPE_TRANSFER
	}

	return request.ActionType_ACTION_TYPE_ISSUE
}

// FuzzActionDeserializerNoPanic fuzzes ActionDeserializer.DeserializeActions with a
// single, arbitrarily typed action. Beyond the no-panic invariant, it also checks that
// a successful deserialization never fabricates or drops entries: exactly one action
// must come back, and it must be non-nil.
func FuzzActionDeserializerNoPanic(f *testing.F) {
	issueRaw := validIssueRaw(f, rp.RangeProofType)
	issueRawCSP := validIssueRaw(f, rp.CSPRangeProofType)
	transferRaw := validTransferRaw(f, rp.RangeProofType, false)
	transferRawCSP := validTransferRaw(f, rp.CSPRangeProofType, false)
	redeemRaw := validTransferRaw(f, rp.RangeProofType, true)

	f.Add(uint8(0), issueRaw)
	f.Add(uint8(0), issueRawCSP)
	f.Add(uint8(1), transferRaw)
	f.Add(uint8(1), transferRawCSP)
	f.Add(uint8(1), redeemRaw)
	f.Add(uint8(0), []byte{})
	f.Add(uint8(1), []byte{})
	f.Add(uint8(0), []byte("malformed"))
	f.Add(uint8(1), []byte("malformed"))
	// Truncated but non-empty protobuf: exercises partial-decode error paths.
	f.Add(uint8(0), issueRaw[:len(issueRaw)/2])
	f.Add(uint8(1), transferRaw[:len(transferRaw)/2])

	f.Fuzz(func(t *testing.T, actionKind uint8, raw []byte) {
		if len(raw) > maxFuzzActionBytes {
			t.Skip()
		}
		typeID := actionTypeFor(actionKind)
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: typeID, Raw: raw}}}

		require.NotPanics(t, func() {
			issues, transfers, err := (&validator.ActionDeserializer{}).DeserializeActions(tokenRequest)
			if err != nil {
				return
			}
			require.Equal(t, 1, len(issues)+len(transfers))
			switch typeID {
			case request.ActionType_ACTION_TYPE_ISSUE:
				require.Len(t, issues, 1)
				require.NotNil(t, issues[0])
			case request.ActionType_ACTION_TYPE_TRANSFER:
				require.Len(t, transfers, 1)
				require.NotNil(t, transfers[0])
			}
		})
	})
}

// FuzzActionDeserializerMultiActionNoPanic fuzzes DeserializeActions with two
// independently typed and independently fuzzed actions in the same request. This
// exercises the type-partitioning done by TokenRequest.GetIssues/GetTransfers across
// mixed-type action lists, which a single-action target cannot reach.
func FuzzActionDeserializerMultiActionNoPanic(f *testing.F) {
	issueRaw := validIssueRaw(f, rp.RangeProofType)
	transferRaw := validTransferRaw(f, rp.RangeProofType, false)

	f.Add(uint8(0), issueRaw, uint8(1), transferRaw)
	f.Add(uint8(1), transferRaw, uint8(0), issueRaw)
	f.Add(uint8(0), issueRaw, uint8(0), issueRaw)
	f.Add(uint8(1), transferRaw, uint8(1), transferRaw)
	f.Add(uint8(1), []byte("malformed"), uint8(0), issueRaw)
	f.Add(uint8(0), issueRaw, uint8(1), []byte{})

	f.Fuzz(func(t *testing.T, kind1 uint8, raw1 []byte, kind2 uint8, raw2 []byte) {
		if len(raw1) > maxFuzzActionBytes || len(raw2) > maxFuzzActionBytes {
			t.Skip()
		}
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{
			{Type: actionTypeFor(kind1), Raw: raw1},
			{Type: actionTypeFor(kind2), Raw: raw2},
		}}

		require.NotPanics(t, func() {
			issues, transfers, err := (&validator.ActionDeserializer{}).DeserializeActions(tokenRequest)
			if err != nil {
				return
			}
			require.LessOrEqual(t, len(issues)+len(transfers), 2)
		})
	})
}

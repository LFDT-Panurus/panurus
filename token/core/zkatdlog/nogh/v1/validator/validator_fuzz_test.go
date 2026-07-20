/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	fv1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	nghactions "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driverv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
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

// issueRawWithNilOutputData returns the serialized bytes of an issue action whose sole
// output commitment is nil. Action.Serialize encodes a nil Data as a G1 proto with an
// empty Raw field (utils.ToProtoG1), which utils.FromG1Proto then decodes back to nil
// without error on the other side (empty Raw is treated as "absent", not malformed).
// This is exactly the wire shape that let a nil commitment reach ZK verification and
// panic before issue/action.go's Validate/GetCommitments guards were added.
func issueRawWithNilOutputData(tb testing.TB) []byte {
	tb.Helper()
	raw, err := (&issue.Action{
		Issuer: []byte("issuer"),
		Outputs: []*token.Token{
			{Owner: []byte("owner-1"), Data: nil},
		},
		ProofType: rp.RangeProofType,
		Proof:     []byte("proof"),
	}).Serialize()
	require.NoError(tb, err)

	return raw
}

// FuzzIssueActionValidateNoPanic fuzzes issue.Action.Deserialize followed by Validate
// and GetCommitments on arbitrary bytes. These are the exact calls IssueValidate makes
// before any issuer-authorization or signature check runs, so a nil-deref anywhere in
// this chain is an unauthenticated validator DoS. The seed corpus includes a
// nil-output-commitment action (the fixed bug) so a regression is caught immediately.
func FuzzIssueActionValidateNoPanic(f *testing.F) {
	f.Add(validIssueRaw(f, rp.RangeProofType))
	f.Add(validIssueRaw(f, rp.CSPRangeProofType))
	f.Add(issueRawWithNilOutputData(f))
	f.Add([]byte{})
	f.Add([]byte("malformed"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzActionBytes {
			t.Skip()
		}

		require.NotPanics(t, func() {
			action := &issue.Action{}
			if err := action.Deserialize(raw); err != nil {
				return
			}
			if err := action.Validate(); err != nil {
				return
			}
			_, _ = action.GetCommitments()
		})
	})
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

	limits := driver.DefaultResourceLimits()
	f.Fuzz(func(t *testing.T, actionKind uint8, raw []byte) {
		if len(raw) > limits.MaxActionBytes {
			t.Skip()
		}
		typeID := actionTypeFor(actionKind)
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: typeID, Raw: raw}}}

		require.NotPanics(t, func() {
			issues, transfers, err := (&validator.ActionDeserializer{Limits: limits}).DeserializeActions(tokenRequest)
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

	limits := driver.DefaultResourceLimits()
	f.Fuzz(func(t *testing.T, kind1 uint8, raw1 []byte, kind2 uint8, raw2 []byte) {
		if len(raw1) > limits.MaxActionBytes || len(raw2) > limits.MaxActionBytes {
			t.Skip()
		}
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{
			{Type: actionTypeFor(kind1), Raw: raw1},
			{Type: actionTypeFor(kind2), Raw: raw2},
		}}

		require.NotPanics(t, func() {
			issues, transfers, err := (&validator.ActionDeserializer{Limits: limits}).DeserializeActions(tokenRequest)
			if err != nil {
				return
			}
			require.LessOrEqual(t, len(issues)+len(transfers), 2)
		})
	})
}

// marshalFuzzedIssueAction builds the raw protobuf bytes of an issue action with the given
// input/output/proof-byte counts, bypassing issue.Action.Serialize so that out-of-limit shapes
// (which Serialize's own caller never produces) can still be exercised.
func marshalFuzzedIssueAction(inputs, outputs, proofBytes int) []byte {
	ia := &nghactions.IssueAction{
		Version: issue.ProtocolV1,
		Issuer:  &driverv1.Identity{Raw: []byte("issuer")},
	}
	for range inputs {
		ia.Inputs = append(ia.Inputs, &nghactions.IssueActionInput{Token: []byte("t")})
	}
	for range outputs {
		ia.Outputs = append(ia.Outputs, &nghactions.IssueActionOutput{Token: &nghactions.Token{Owner: []byte("o")}})
	}
	if proofBytes > 0 {
		ia.Proof = &nghactions.Proof{ProofType: &nghactions.Proof_Proof{Proof: make([]byte, proofBytes)}}
	}
	raw, _ := proto.Marshal(ia)

	return raw
}

// marshalFuzzedTransferAction mirrors marshalFuzzedIssueAction for transfer actions.
func marshalFuzzedTransferAction(inputs, outputs, proofBytes int) []byte {
	ta := &nghactions.TransferAction{
		Version: transfer.ProtocolV1,
	}
	for range inputs {
		ta.Inputs = append(ta.Inputs, &nghactions.TransferActionInput{Input: &nghactions.Token{Owner: []byte("o")}})
	}
	for range outputs {
		ta.Outputs = append(ta.Outputs, &nghactions.TransferActionOutput{Token: &nghactions.Token{Owner: []byte("o")}})
	}
	if proofBytes > 0 {
		ta.Proof = &nghactions.Proof{ProofType: &nghactions.Proof_Proof{Proof: make([]byte, proofBytes)}}
	}
	raw, _ := proto.Marshal(ta)

	return raw
}

// boundInt clamps n into [lo, hi], using n's magnitude modulo the range width so that fuzzed
// values (including negatives) still exercise the full range deterministically.
func boundInt(n, lo, hi int) int {
	if hi <= lo {
		return lo
	}
	width := hi - lo + 1
	if n < 0 {
		n = -n
	}

	return lo + n%width
}

// FuzzActionResourceLimits fuzzes issue and transfer actions shaped by their resource
// dimensions (input/output counts, proof byte length) and asserts that Deserialize never
// panics and rejects any dimension that exceeds its configured limit with the corresponding
// typed error. Rejection-before-cryptographic-work is a timing property and is covered
// separately by the dedicated RejectsBeforeCryptographicWork unit tests, which run without
// fuzz-worker CPU contention.
func FuzzActionResourceLimits(f *testing.F) {
	limits := driver.DefaultResourceLimits()
	f.Add(true, 1, 1, 8)
	f.Add(true, limits.MaxInputs, 1, 8)
	f.Add(true, limits.MaxInputs+1, 1, 8)
	f.Add(true, 1, limits.MaxOutputs, 8)
	f.Add(true, 1, limits.MaxOutputs+1, 8)
	f.Add(true, 1, 1, limits.MaxProofBytes)
	f.Add(true, 1, 1, limits.MaxProofBytes+1)
	f.Add(false, 1, 1, 8)
	f.Add(false, limits.MaxInputs, 1, 8)
	f.Add(false, limits.MaxInputs+1, 1, 8)
	f.Add(false, 1, limits.MaxOutputs, 8)
	f.Add(false, 1, limits.MaxOutputs+1, 8)
	f.Add(false, 1, 1, limits.MaxProofBytes)
	f.Add(false, 1, 1, limits.MaxProofBytes+1)

	f.Fuzz(func(t *testing.T, isIssue bool, inputs, outputs, proofBytes int) {
		inputs = boundInt(inputs, 1, 512)
		outputs = boundInt(outputs, 1, 512)
		proofBytes = boundInt(proofBytes, 1, 256<<10)

		var raw []byte
		maxInputs, maxOutputs, maxProofBytes := limits.MaxInputs, limits.MaxOutputs, limits.MaxProofBytes
		var errTooManyInputs, errTooManyOutputs, errProofTooLarge error
		if isIssue {
			raw = marshalFuzzedIssueAction(inputs, outputs, proofBytes)
			errTooManyInputs, errTooManyOutputs, errProofTooLarge = issue.ErrTooManyInputs, issue.ErrTooManyOutputs, issue.ErrProofTooLarge
		} else {
			raw = marshalFuzzedTransferAction(inputs, outputs, proofBytes)
			errTooManyInputs, errTooManyOutputs, errProofTooLarge = transfer.ErrTooManyInputs, transfer.ErrTooManyOutputs, transfer.ErrProofTooLarge
		}

		var err error
		require.NotPanics(t, func() {
			if isIssue {
				err = (&issue.Action{}).Deserialize(raw)
			} else {
				err = (&transfer.Action{}).Deserialize(raw)
			}
		})

		switch {
		case inputs > maxInputs:
			require.ErrorIs(t, err, errTooManyInputs)
		case outputs > maxOutputs:
			require.ErrorIs(t, err, errTooManyOutputs)
		case proofBytes > maxProofBytes:
			require.ErrorIs(t, err, errProofTooLarge)
		}
	})
}

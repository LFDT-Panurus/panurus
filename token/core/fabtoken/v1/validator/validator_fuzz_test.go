/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"testing"

	fbactions "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driverv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

func FuzzActionDeserializerNoPanic(f *testing.F) {
	issueRaw, err := (&actions.IssueAction{Issuer: []byte("issuer")}).Serialize()
	require.NoError(f, err)
	transferRaw, err := (&actions.TransferAction{}).Serialize()
	require.NoError(f, err)
	f.Add(uint8(0), issueRaw)
	f.Add(uint8(1), transferRaw)
	f.Add(uint8(1), []byte("malformed"))

	limits := driver.DefaultResourceLimits()
	f.Fuzz(func(t *testing.T, actionKind uint8, raw []byte) {
		if len(raw) > limits.MaxActionBytes {
			t.Skip()
		}
		typeID := request.ActionType_ACTION_TYPE_ISSUE
		if actionKind%2 == 1 {
			typeID = request.ActionType_ACTION_TYPE_TRANSFER
		}
		tokenRequest := &driver.TokenRequest{Actions: []*driver.TypedAction{{Type: typeID, Raw: raw}}}

		require.NotPanics(t, func() {
			_, _, _ = (&validator.ActionDeserializer{Limits: limits}).DeserializeActions(tokenRequest)
		})
	})
}

// marshalFuzzedIssueAction builds the raw protobuf bytes of an issue action with the given
// output count, bypassing IssueAction.Serialize so that out-of-limit shapes (which Serialize's
// own caller never produces) can still be exercised.
func marshalFuzzedIssueAction(outputs int) []byte {
	ia := &fbactions.IssueAction{
		Version: actions.ProtocolV1,
		Issuer:  &driverv1.Identity{Raw: []byte("issuer")},
	}
	for range outputs {
		ia.Outputs = append(ia.Outputs, &fbactions.IssueActionOutput{Token: &fbactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	raw, _ := proto.Marshal(ia)

	return raw
}

// marshalFuzzedTransferAction mirrors marshalFuzzedIssueAction for transfer actions.
func marshalFuzzedTransferAction(inputs, outputs int) []byte {
	ta := &fbactions.TransferAction{
		Version: actions.ProtocolV1,
	}
	for range inputs {
		ta.Inputs = append(ta.Inputs, &fbactions.TransferActionInput{Input: &fbactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
	}
	for range outputs {
		ta.Outputs = append(ta.Outputs, &fbactions.TransferActionOutput{Token: &fbactions.Token{Owner: []byte("o"), Type: "TYPE", Quantity: "0x1"}})
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
// dimensions (input/output counts) and asserts that Deserialize never panics and rejects
// any dimension that exceeds its configured limit with the corresponding typed error.
func FuzzActionResourceLimits(f *testing.F) {
	limits := driver.DefaultResourceLimits()
	f.Add(true, 1, 1)
	f.Add(true, 1, limits.MaxOutputs)
	f.Add(true, 1, limits.MaxOutputs+1)
	f.Add(false, 1, 1)
	f.Add(false, limits.MaxInputs, 1)
	f.Add(false, limits.MaxInputs+1, 1)
	f.Add(false, 1, limits.MaxOutputs)
	f.Add(false, 1, limits.MaxOutputs+1)

	f.Fuzz(func(t *testing.T, isIssue bool, inputs, outputs int) {
		inputs = boundInt(inputs, 1, 512)
		outputs = boundInt(outputs, 1, 512)

		var raw []byte
		var err error
		if isIssue {
			raw = marshalFuzzedIssueAction(outputs)
			require.NotPanics(t, func() {
				err = (&actions.IssueAction{}).Deserialize(raw)
			})
			if outputs > limits.MaxOutputs {
				require.ErrorIs(t, err, actions.ErrTooManyOutputs)
			}

			return
		}

		raw = marshalFuzzedTransferAction(inputs, outputs)
		require.NotPanics(t, func() {
			err = (&actions.TransferAction{}).Deserialize(raw)
		})
		switch {
		case inputs > limits.MaxInputs:
			require.ErrorIs(t, err, actions.ErrTooManyInputs)
		case outputs > limits.MaxOutputs:
			require.ErrorIs(t, err, actions.ErrTooManyOutputs)
		}
	})
}

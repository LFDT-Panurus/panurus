/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
	"github.com/stretchr/testify/require"
)

// boundLimitDim maps n into [lo, hi].
func boundLimitDim(n, lo, hi int) int {
	width := hi - lo + 1
	n %= width
	if n < 0 {
		n += width
	}

	return lo + n
}

// fuzzLimitsMetadata returns exactly entries distinct metadata entries whose
// keys and values stay well within the metadata key/value size limits.
func fuzzLimitsMetadata(entries int) map[string][]byte {
	if entries == 0 {
		return nil
	}
	m := make(map[string][]byte, entries)
	for i := range entries {
		m[strconv.Itoa(i)] = nil
	}

	return m
}

// fuzzLimitsOutputs returns n minimal output descriptions.
func fuzzLimitsOutputs(n int) []snarktoken.OutputDescription {
	outputs := make([]snarktoken.OutputDescription, n)
	for i := range outputs {
		outputs[i] = snarktoken.OutputDescription{CommitmentOut: make([]byte, 32), OutputProof: []byte{1}}
	}

	return outputs
}

// marshalLimitsIssueAction serializes an issue action with the given number of
// upgrade inputs, outputs and metadata entries.
func marshalLimitsIssueAction(tb testing.TB, inputs, outputs, metadataEntries int) []byte {
	tb.Helper()
	action := &snarktoken.IssueAction{
		Issuer:         []byte("issuer"),
		TokenType:      "USD",
		TypeCommitment: make([]byte, 32),
		Outputs:        fuzzLimitsOutputs(outputs),
		Metadata:       fuzzLimitsMetadata(metadataEntries),
	}
	for i := range inputs {
		// a non-nil ID, so ErrInvalidUpgradeInput does not fire before the limits
		action.Inputs = append(action.Inputs, &snarktoken.IssueActionInput{
			ID:    token2.ID{TxId: "tx", Index: uint64(i)},
			Token: []byte{1},
		})
	}
	raw, err := action.Serialize()
	require.NoError(tb, err)

	return raw
}

// marshalLimitsTransferAction serializes a transfer action with the given
// number of inputs, outputs and metadata entries.
func marshalLimitsTransferAction(tb testing.TB, inputs, outputs, metadataEntries int) []byte {
	tb.Helper()
	action := &snarktoken.TransferAction{
		TypeCommitment: make([]byte, 32),
		Inputs:         make([]snarktoken.SpendDescription, inputs),
		Outputs:        fuzzLimitsOutputs(outputs),
		Metadata:       fuzzLimitsMetadata(metadataEntries),
	}
	for i := range action.Inputs {
		action.Inputs[i] = snarktoken.SpendDescription{CommitmentIn: make([]byte, 32), SpendProof: []byte{1}}
	}
	raw, err := action.Serialize()
	require.NoError(tb, err)

	return raw
}

// expectedLimitError returns the typed error that the limit checks report for
// the given dimensions, in the order they run (inputs, outputs, metadata), or
// nil when every dimension is within its limit.
func expectedLimitError(inputs, outputs, metadataEntries int, limits driver.ResourceLimits) error {
	switch {
	case inputs > limits.MaxInputs:
		return snarktoken.ErrTooManyInputs
	case outputs > limits.MaxOutputs:
		return snarktoken.ErrTooManyOutputs
	case metadataEntries > limits.MaxMetadataEntries:
		return snarktoken.ErrTooManyMetadataEntries
	default:
		return nil
	}
}

// FuzzActionResourceLimits fuzzes zkatsnark issue and transfer actions shaped by
// their resource dimensions (input count, output count, metadata entries) and
// runs them through the validator's ActionDeserializer, the path that stamps
// the configured limits onto each action. It asserts that deserialization never
// panics, rejects any dimension that exceeds its limit with the corresponding
// typed error, and accepts every action at or below all limits. Both the
// default limits and a small custom set, as a TMS configuration would pass, are
// checked for every input.
//
// Unlike zkatdlog, proof bytes are intentionally not a dimension: zkatsnark
// enforces no MaxProofBytes, since a single bound would reject every plonkfri
// proof (see docs/drivers/zkatsnark.md).
func FuzzActionResourceLimits(f *testing.F) {
	defaults := driver.DefaultResourceLimits()
	for _, isIssue := range []bool{true, false} {
		f.Add(isIssue, 1, 1, 0)
		f.Add(isIssue, defaults.MaxInputs, 1, 0)
		f.Add(isIssue, defaults.MaxInputs+1, 1, 0)
		f.Add(isIssue, 1, defaults.MaxOutputs, 0)
		f.Add(isIssue, 1, defaults.MaxOutputs+1, 0)
		f.Add(isIssue, 1, 1, defaults.MaxMetadataEntries)
		f.Add(isIssue, 1, 1, defaults.MaxMetadataEntries+1)
	}
	// a plain issue with no upgrade inputs
	f.Add(true, 0, 1, 0)

	custom := driver.ResourceLimits{MaxInputs: 3, MaxOutputs: 4, MaxMetadataEntries: 2}.WithDefaults()
	limitSets := []driver.ResourceLimits{defaults, custom}

	f.Fuzz(func(t *testing.T, isIssue bool, inputs, outputs, metadataEntries int) {
		inputs = boundLimitDim(inputs, 0, 512)
		outputs = boundLimitDim(outputs, 0, 512)
		metadataEntries = boundLimitDim(metadataEntries, 0, 128)

		action := &driver.TypedAction{Type: request.ActionType_ACTION_TYPE_TRANSFER}
		if isIssue {
			action.Type = request.ActionType_ACTION_TYPE_ISSUE
			action.Raw = marshalLimitsIssueAction(t, inputs, outputs, metadataEntries)
		} else {
			action.Raw = marshalLimitsTransferAction(t, inputs, outputs, metadataEntries)
		}
		tr := &driver.TokenRequest{Actions: []*driver.TypedAction{action}}

		for _, limits := range limitSets {
			var err error
			require.NotPanics(t, func() {
				_, _, err = (&validator.ActionDeserializer{Limits: limits}).DeserializeActions(tr)
			})
			if want := expectedLimitError(inputs, outputs, metadataEntries, limits); want != nil {
				require.ErrorIs(t, err, want, "limits %+v", limits)

				continue
			}
			require.NoError(t, err, "limits %+v", limits)
		}
	})
}

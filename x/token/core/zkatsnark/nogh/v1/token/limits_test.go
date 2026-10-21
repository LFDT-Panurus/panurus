/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"strings"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/stretchr/testify/require"
)

func TestActionMetadata(t *testing.T) {
	t.Parallel()

	md := map[string][]byte{"k": []byte("v")}
	ta := &token.TransferAction{Outputs: []token.OutputDescription{{}}, Metadata: md}
	ia := &token.IssueAction{Outputs: []token.OutputDescription{{}}, Metadata: md}
	require.Equal(t, md, ta.GetMetadata())
	require.Equal(t, md, ia.GetMetadata())

	raw, err := ta.Serialize()
	require.NoError(t, err)
	ta2 := &token.TransferAction{}
	require.NoError(t, ta2.Deserialize(raw))
	require.Equal(t, md, ta2.GetMetadata())

	raw, err = ia.Serialize()
	require.NoError(t, err)
	ia2 := &token.IssueAction{}
	require.NoError(t, ia2.Deserialize(raw))
	require.Equal(t, md, ia2.GetMetadata())
}

func TestActionLimits(t *testing.T) {
	t.Parallel()

	limits := driver.ResourceLimits{MaxInputs: 1, MaxOutputs: 1, MaxMetadataEntries: 1, MaxMetadataKeyBytes: 2, MaxMetadataValueBytes: 2}
	out := token.OutputDescription{}

	tests := []struct {
		name   string
		in     []token.SpendDescription
		outs   []token.OutputDescription
		md     map[string][]byte
		target error
	}{
		{"ok", []token.SpendDescription{{}}, []token.OutputDescription{out}, map[string][]byte{"k": []byte("v")}, nil},
		{"too many inputs", []token.SpendDescription{{}, {}}, []token.OutputDescription{out}, nil, token.ErrTooManyInputs},
		{"too many outputs", []token.SpendDescription{{}}, []token.OutputDescription{out, out}, nil, token.ErrTooManyOutputs},
		{"too many entries", []token.SpendDescription{{}}, []token.OutputDescription{out}, map[string][]byte{"a": nil, "b": nil}, token.ErrTooManyMetadataEntries},
		{"key too large", []token.SpendDescription{{}}, []token.OutputDescription{out}, map[string][]byte{"abc": nil}, token.ErrMetadataKeyTooLarge},
		{"value too large", []token.SpendDescription{{}}, []token.OutputDescription{out}, map[string][]byte{"a": []byte(strings.Repeat("x", 3))}, token.ErrMetadataValueTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ta := &token.TransferAction{Inputs: tc.in, Outputs: tc.outs, Metadata: tc.md}
			ta.SetLimits(limits)
			check(t, tc.target, ta.Validate())
			raw, err := ta.Serialize()
			require.NoError(t, err)
			ta2 := &token.TransferAction{}
			ta2.SetLimits(limits)
			check(t, tc.target, ta2.Deserialize(raw))

			if len(tc.in) > 1 {
				return // issue actions have no inputs
			}
			ia := &token.IssueAction{Outputs: tc.outs, Metadata: tc.md}
			ia.SetLimits(limits)
			check(t, tc.target, ia.Validate())
			raw, err = ia.Serialize()
			require.NoError(t, err)
			ia2 := &token.IssueAction{}
			ia2.SetLimits(limits)
			check(t, tc.target, ia2.Deserialize(raw))
		})
	}
}

func check(t *testing.T, target, err error) {
	t.Helper()
	if target == nil {
		require.NoError(t, err)

		return
	}
	require.ErrorIs(t, err, target)
}

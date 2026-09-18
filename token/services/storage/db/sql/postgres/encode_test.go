/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"fmt"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/stretchr/testify/require"
)

var someCompositeKey = utils.MustGet(kvs.CreateCompositeKey("prefix", []string{"a", "b", "c"}))

func TestDecodeBYTEA(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantOutput  string
		expectError bool
	}{
		{
			name:       "no hex returns unchanged",
			input:      "hello",
			wantOutput: "hello",
		},
		{
			name:       "decode valid hex",
			input:      "\\x68656c6c6f", // "hello"
			wantOutput: "hello",
		},
		{
			name:        "invalid hex returns error",
			input:       "\\xzzzz",
			expectError: true,
		},
		{
			name:       "prefix but empty hex",
			input:      "\\x",
			wantOutput: "", // empty decode
		},
		{
			name:       "composite key",
			input:      fmt.Sprintf("\\x%x", someCompositeKey),
			wantOutput: someCompositeKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeBYTEA(tc.input)
			if tc.expectError {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOutput, got)
		})
	}
}

func TestEncoding(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"basic ascii", "hello"},
		{"empty string", ""},
		{"unicode", "😀✓漢字"},
		{"whitespace", "  spaced\t\n"},
		{"long string", string(make([]byte, 1024))},
		{"composite key", someCompositeKey},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := identity(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.input, got)
		})
	}
}

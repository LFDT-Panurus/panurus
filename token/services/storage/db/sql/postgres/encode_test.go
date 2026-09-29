/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/stretchr/testify/require"
)

var someCompositeKey = utils.MustGet(kvs.CreateCompositeKey("prefix", []string{"a", "b", "c"}))

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

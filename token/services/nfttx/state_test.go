/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"encoding/base64"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encodeRaw(payload string) token2.Type {
	return token2.Type(base64.StdEncoding.EncodeToString([]byte(payload)))
}

func TestDecodeState(t *testing.T) {
	h := &House{LinearID: "123", Address: "5th Avenue", Valuation: 100}

	var house House
	require.NoError(t, decodeState(encodeState(t, h), &house))
	assert.Equal(t, *h, house)
}

func TestDecodeState_Errors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   token2.Type
		error string
	}{
		{"not base64", "not-base64", "failed to decode type"},
		{"malformed json", encodeRaw("{not json"), "failed to unmarshal state"},
		{"trailing garbage", encodeRaw(`{"LinearID":"123"} and more`), "failed to unmarshal state"},
		{"type mismatch", encodeRaw(`{"Valuation":"not-a-number"}`), "failed to unmarshal state"},
		{"number out of range", encodeRaw(`{"Valuation":99999999999999999999}`), "failed to unmarshal state"},
		{"null", encodeRaw("null"), "type carries no state"},
		{"null padded", encodeRaw("  null\n"), "type carries no state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var house House
			require.ErrorContains(t, decodeState(tc.typ, &house), tc.error)
			assert.Equal(t, House{}, house, "state must be left untouched on error")
		})
	}
}

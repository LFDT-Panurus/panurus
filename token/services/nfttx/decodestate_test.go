/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type House struct {
	LinearID  string
	Address   string
	Valuation uint64
}

func encodeState(t *testing.T, v any) token2.Type {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return token2.Type(base64.StdEncoding.EncodeToString(raw))
}

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

// must never report a success it cannot back up
func FuzzDecodeStateNoPanic(f *testing.F) {
	valid, err := json.Marshal(&House{LinearID: "123", Address: "5th Avenue", Valuation: 100})
	require.NoError(f, err)

	f.Add([]byte(base64.StdEncoding.EncodeToString(valid)))
	f.Add([]byte{})
	f.Add([]byte("not-base64"))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte("{not json"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte(`{"Valuation":"not-a-number"}`))))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte("null"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString([]byte("[]"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString(valid))[:len(valid)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		var house House
		if err := decodeState(token2.Type(raw), &house); err != nil {
			return
		}

		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		require.NoError(t, err, "decodeState succeeded on a type that is not valid base64")

		require.False(t, bytes.Equal(bytes.TrimSpace(decoded), []byte("null")),
			"decodeState succeeded on a payload carrying no state")

		var again House
		require.NoError(t, json.Unmarshal(decoded, &again),
			"decodeState succeeded on a payload that does not unmarshal")
		require.Equal(t, again, house)
	})
}

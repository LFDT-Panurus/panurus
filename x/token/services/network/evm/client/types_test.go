/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHexToAddress(t *testing.T) {
	const canonical = "0x1234567890123456789012345678901234567890"

	tests := []struct {
		name    string
		in      string
		wantErr bool
		wantHex string
	}{
		{name: "0x prefix", in: canonical, wantHex: canonical},
		{name: "no prefix", in: "1234567890123456789012345678901234567890", wantHex: canonical},
		{name: "uppercase", in: "0xABCDEF0000000000000000000000000000000000", wantHex: "0xabcdef0000000000000000000000000000000000"},
		{name: "whitespace", in: "  " + canonical + "  ", wantHex: canonical},
		{name: "too short", in: "0x1234", wantErr: true},
		{name: "too long", in: canonical + "00", wantErr: true},
		{name: "not hex", in: "0xZZ34567890123456789012345678901234567890", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := HexToAddress(tc.in)
			if tc.wantErr {
				assert.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantHex, a.Hex())
		})
	}
}

func TestAddressRoundTrip(t *testing.T) {
	a, err := HexToAddress("0x1234567890123456789012345678901234567890")
	require.NoError(t, err)

	assert.Len(t, a.Bytes(), AddressLength)
	assert.Equal(t, a.Hex(), a.String())

	// text round-trip
	text, err := a.MarshalText()
	require.NoError(t, err)
	var fromText Address
	require.NoError(t, fromText.UnmarshalText(text))
	assert.Equal(t, a, fromText)

	// JSON round-trip
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	assert.JSONEq(t, `"0x1234567890123456789012345678901234567890"`, string(raw))
	var fromJSON Address
	require.NoError(t, json.Unmarshal(raw, &fromJSON))
	assert.Equal(t, a, fromJSON)
}

func TestBytesToAddressRightAligns(t *testing.T) {
	// shorter than 20 bytes: left-padded with zeros
	short := BytesToAddress([]byte{0x01, 0x02})
	assert.Equal(t, "0x0000000000000000000000000000000000000102", short.Hex())

	// longer than 20 bytes: low-order 20 bytes kept
	long := make([]byte, 22)
	long[0], long[1] = 0xff, 0xff // these must be dropped
	long[21] = 0x09
	assert.Equal(t, "0x0000000000000000000000000000000000000009", BytesToAddress(long).Hex())
}

func TestHexToHash(t *testing.T) {
	const canonical = "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"

	h, err := HexToHash(canonical)
	require.NoError(t, err)
	assert.Equal(t, canonical, h.Hex())
	assert.Len(t, h.Bytes(), HashLength)

	_, err = HexToHash("0xdeadbeef")
	assert.Error(t, err, "hash shorter than 32 bytes must be rejected")
}

func TestHashRoundTrip(t *testing.T) {
	h, err := HexToHash("0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470")
	require.NoError(t, err)

	raw, err := json.Marshal(h)
	require.NoError(t, err)
	var fromJSON Hash
	require.NoError(t, json.Unmarshal(raw, &fromJSON))
	assert.Equal(t, h, fromJSON)
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package abi

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMethodID pins the selectors against the values reproducible with any Ethereum tooling
// (cast sig "getToken(bytes32)"), so a wrong keccak input cannot pass unnoticed.
func TestMethodID(t *testing.T) {
	// Independently verified with `cast sig`.
	golden := map[string]string{
		"getToken(bytes32)":        "155bf4e2",
		"getPublicParameters()":    "93ab0b72",
		"getPublicParamsVersion()": "58d9ccdd",
	}
	for sig, want := range golden {
		assert.Equal(t, want, hex.EncodeToString(MethodID(sig)), "selector for %s", sig)
	}
}

func TestEncodeBytes32Call(t *testing.T) {
	var arg [32]byte
	arg[31] = 0xAB
	got := EncodeBytes32Call("getToken(bytes32)", arg)

	require.Len(t, got, 4+32)
	assert.Equal(t, MethodID("getToken(bytes32)"), got[:4])
	assert.Equal(t, arg[:], got[4:], "bytes32 argument is stored as-is, no padding")
}

func TestDecodeBytes(t *testing.T) {
	t.Run("round-trips arbitrary payloads", func(t *testing.T) {
		for _, payload := range [][]byte{
			{},
			{0x01},
			[]byte("a 40-byte payload spanning two ABI words!"),
			make([]byte, 96),
		} {
			assertDecodesTo(t, encodeBytes(payload), payload)
		}
	})

	t.Run("rejects a truncated offset word", func(t *testing.T) {
		_, err := DecodeBytes(make([]byte, 8))
		require.Error(t, err)
	})

	t.Run("rejects an out-of-bounds offset", func(t *testing.T) {
		enc := encodeBytes([]byte("x"))
		// corrupt the offset word to point past the end
		enc[31] = 0xff
		_, err := DecodeBytes(enc)
		require.Error(t, err)
	})

	t.Run("rejects a length past the buffer", func(t *testing.T) {
		enc := encodeBytes([]byte("x"))
		// length word is the second word; inflate it well beyond the data
		enc[63] = 0x7f
		_, err := DecodeBytes(enc)
		require.Error(t, err)
	})
}

func TestDecodeUint64(t *testing.T) {
	t.Run("decodes a right-aligned value", func(t *testing.T) {
		word := make([]byte, 32)
		word[31] = 0x2a
		v, err := DecodeUint64(word)
		require.NoError(t, err)
		assert.Equal(t, uint64(42), v)
	})

	t.Run("decodes the max uint64", func(t *testing.T) {
		word := make([]byte, 32)
		for i := 24; i < 32; i++ {
			word[i] = 0xff
		}
		v, err := DecodeUint64(word)
		require.NoError(t, err)
		assert.Equal(t, uint64(0xffffffffffffffff), v)
	})

	t.Run("rejects a value wider than 64 bits", func(t *testing.T) {
		word := make([]byte, 32)
		word[23] = 0x01 // a bit above the low 8 bytes
		_, err := DecodeUint64(word)
		require.Error(t, err)
	})

	t.Run("rejects a short buffer", func(t *testing.T) {
		_, err := DecodeUint64(make([]byte, 16))
		require.Error(t, err)
	})
}

func assertDecodesTo(t *testing.T, enc, want []byte) {
	t.Helper()
	got, err := DecodeBytes(enc)
	require.NoError(t, err)
	if len(want) == 0 {
		assert.Empty(t, got)

		return
	}
	assert.Equal(t, want, got)
}

// encodeBytes produces the canonical ABI encoding of a single dynamic `bytes` value: an offset word
// (always 0x20 here), a length word, then the right-padded data. It mirrors what an EVM node returns
// for a `returns (bytes)` method, so DecodeBytes is exercised against real-shaped input.
func encodeBytes(b []byte) []byte {
	out := make([]byte, 64)
	out[31] = 0x20 // offset to the tail
	binary.BigEndian.PutUint64(out[56:64], uint64(len(b)))
	out = append(out, b...)
	if pad := (32 - len(b)%32) % 32; pad != 0 {
		out = append(out, make([]byte, pad)...)
	}

	return out
}

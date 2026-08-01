/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRLPStringVectors pins the encoder against the canonical examples from the Ethereum yellow
// paper / RLP spec, so a wrong prefix or length form cannot pass unnoticed.
func TestRLPStringVectors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty string", nil, "80"},
		{"single zero byte", []byte{0x00}, "00"},
		{"single low byte", []byte{0x7f}, "7f"},
		{"single byte at boundary", []byte{0x80}, "8180"},
		{"dog", []byte("dog"), "83646f67"},
		{"55 bytes uses short form", bytes.Repeat([]byte{0x61}, 55), "b7" + strings.Repeat("61", 55)},
		{"56 bytes uses long form", bytes.Repeat([]byte{0x61}, 56), "b838" + strings.Repeat("61", 56)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hex.EncodeToString(rlpString(tt.in)))
		})
	}
}

func TestRLPListVectors(t *testing.T) {
	// ["cat","dog"] = 0xc8 0x83 c a t 0x83 d o g
	payload := append(rlpString([]byte("cat")), rlpString([]byte("dog"))...)
	assert.Equal(t, "c88363617483646f67", hex.EncodeToString(rlpList(payload)))

	// the empty list
	assert.Equal(t, "c0", hex.EncodeToString(rlpList(nil)))
}

// TestRLPUintIsMinimal checks the canonical integer form: no leading zeros, and zero encodes as the
// empty string. A non-minimal encoding would change the signing hash and the node would reject the
// transaction.
func TestRLPUintIsMinimal(t *testing.T) {
	cases := map[uint64]string{
		0:          "80",
		1:          "01",
		127:        "7f",
		128:        "8180",
		256:        "820100",
		1024:       "820400",
		16_777_216: "8401000000",
	}
	for in, want := range cases {
		assert.Equal(t, want, hex.EncodeToString(rlpUint(in)), "rlpUint(%d)", in)
	}
}

func TestRLPBigIntMatchesUint(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 256, 1 << 32} {
		assert.Equal(t,
			hex.EncodeToString(rlpUint(v)),
			hex.EncodeToString(rlpBigInt(new(big.Int).SetUint64(v))),
			"big and uint encodings must agree for %d", v)
	}
	// nil is treated as zero
	assert.Equal(t, "80", hex.EncodeToString(rlpBigInt(nil)))
}

func TestMinimalBytes(t *testing.T) {
	require.Empty(t, minimalBytes(0))
	assert.Equal(t, []byte{0x01}, minimalBytes(1))
	assert.Equal(t, []byte{0x01, 0x00}, minimalBytes(256))
	assert.Equal(t, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, minimalBytes(^uint64(0)))
}

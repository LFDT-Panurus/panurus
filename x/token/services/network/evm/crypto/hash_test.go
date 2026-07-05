/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"encoding/hex"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeccak256KnownVectors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// Well-known Ethereum keccak-256 vectors.
		{name: "empty", in: "", want: "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{name: "abc", in: "abc", want: "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hex.EncodeToString(Keccak256([]byte(tc.in))))
		})
	}
}

// TestKeccak256Concatenation guards the variadic behaviour: hashing several slices must equal
// hashing their concatenation, because callers rely on that when building composite keys.
func TestKeccak256Concatenation(t *testing.T) {
	assert.Equal(t, Keccak256([]byte("abc")), Keccak256([]byte("a"), []byte("bc")))
	assert.Equal(t, Keccak256([]byte("abc")), Keccak256([]byte("ab"), []byte("c")))
}

func TestKeccak256HashLength(t *testing.T) {
	h := Keccak256Hash([]byte("anything"))
	assert.Len(t, h[:], HashLength)
	assert.Equal(t, Keccak256([]byte("anything")), h[:])
}

func TestSHA256KnownVector(t *testing.T) {
	assert.Equal(t,
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		hex.EncodeToString(SHA256([]byte("abc"))),
	)
}

// TestSHA256MatchesHashable is the parity guard: the driver's SHA256 must equal the SDK's
// Hashable.Raw() for non-empty input, otherwise the token-request and public-parameters hashes
// the driver writes on-chain would not match what the rest of the SDK computes and compares.
func TestSHA256MatchesHashable(t *testing.T) {
	for _, in := range [][]byte{[]byte("abc"), []byte("public-parameters"), []byte("a token request")} {
		require.Equal(t, []byte(utils.Hashable(in).Raw()), SHA256(in))
	}
}

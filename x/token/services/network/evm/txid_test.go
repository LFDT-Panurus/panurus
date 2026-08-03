/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// TestComputeTxIDGeneratesAndWritesBackNonce is the critical mutating contract (design §5.3): callers
// pass a TxID with an empty nonce and rely on the driver to fill it in. A read-only implementation
// would derive the same anchor for every transaction of a creator, and the second one submitted would
// revert on chain as an already-processed anchor.
func TestComputeTxIDGeneratesAndWritesBackNonce(t *testing.T) {
	n := testNetwork(t, nil, nil)

	id := &driver.TxID{Creator: []byte("alice")}
	anchor := n.ComputeTxID(id)

	require.NotEmpty(t, anchor)
	require.Len(t, id.Nonce, NonceLength, "the generated nonce must be written back into the caller's struct")
	assert.NotEqual(t, make([]byte, NonceLength), id.Nonce, "the nonce must not be all zeros")

	// Recomputing with the now-populated id must reproduce the same anchor.
	assert.Equal(t, anchor, n.ComputeTxID(id), "a populated nonce must be respected, not regenerated")
}

// TestComputeTxIDDistinctPerTransaction is the property the on-chain replay guard depends on: two
// transactions from the same creator must get different anchors.
func TestComputeTxIDDistinctPerTransaction(t *testing.T) {
	n := testNetwork(t, nil, nil)

	seen := make(map[string]struct{}, 50)
	for range 50 {
		anchor := n.ComputeTxID(&driver.TxID{Creator: []byte("alice")})
		_, dup := seen[anchor]
		require.False(t, dup, "anchors must be unique per transaction")
		seen[anchor] = struct{}{}
	}
}

// TestComputeTxIDRoundTripsToAnchor checks the anchor is exactly what keys.AnchorFromTxID decodes,
// since the translator and the contract address state by that 32-byte value.
func TestComputeTxIDRoundTripsToAnchor(t *testing.T) {
	n := testNetwork(t, nil, nil)

	id := &driver.TxID{Creator: []byte("alice")}
	anchor := n.ComputeTxID(id)

	decoded, err := keys.AnchorFromTxID(anchor)
	require.NoError(t, err, "the anchor must decode as a 32-byte value")
	assert.Len(t, decoded, keys.AnchorLength)

	raw, err := hex.DecodeString(anchor)
	require.NoError(t, err)
	assert.Equal(t, raw, decoded[:])
}

// TestComputeTxIDIsDeterministic pins the derivation: the same nonce and creator always yield the
// same anchor.
func TestComputeTxIDIsDeterministic(t *testing.T) {
	n := testNetwork(t, nil, nil)

	nonce := bytes.Repeat([]byte{0xAB}, NonceLength)
	first := n.ComputeTxID(&driver.TxID{Nonce: nonce, Creator: []byte("alice")})
	second := n.ComputeTxID(&driver.TxID{Nonce: nonce, Creator: []byte("alice")})
	assert.Equal(t, first, second)
}

// TestComputeTxIDGoldenVector pins the exact derivation, so a change to the hash input (which would
// silently move every anchor, and with it every token id) cannot pass unnoticed.
func TestComputeTxIDGoldenVector(t *testing.T) {
	n := testNetwork(t, nil, nil)

	anchor := n.ComputeTxID(&driver.TxID{
		Nonce:   bytes.Repeat([]byte{0x01}, NonceLength),
		Creator: []byte("creator"),
	})
	assert.Equal(t, "51cce1270715341f3cf0e9e2dde8913c7f0484c64a46c00b332d6befb1761ffa", anchor)
	assert.Len(t, anchor, 64, "an anchor is hex of 32 bytes")
}

// TestComputeTxIDLengthPrefixPreventsAmbiguity checks the reason the nonce is length prefixed rather
// than plainly concatenated (design §5.3): without it, a different split of the same bytes between
// nonce and creator would collide, and since the anchor is the contract's replay key a collision
// makes a legitimate transaction revert.
func TestComputeTxIDLengthPrefixPreventsAmbiguity(t *testing.T) {
	n := testNetwork(t, nil, nil)

	// "ab"+"c" and "a"+"bc" concatenate to the same bytes but must not share an anchor.
	first := n.ComputeTxID(&driver.TxID{Nonce: []byte("ab"), Creator: []byte("c")})
	second := n.ComputeTxID(&driver.TxID{Nonce: []byte("a"), Creator: []byte("bc")})
	assert.NotEqual(t, first, second, "the nonce/creator split must be unambiguous")
}

func TestComputeTxIDNilIsEmpty(t *testing.T) {
	n := testNetwork(t, nil, nil)
	assert.Empty(t, n.ComputeTxID(nil))
}

func TestGenerateNonce(t *testing.T) {
	first, err := generateNonce()
	require.NoError(t, err)
	require.Len(t, first, NonceLength)

	second, err := generateNonce()
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "nonces must be random")
}

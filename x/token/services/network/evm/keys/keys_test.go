/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package keys

import (
	"encoding/hex"
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anchor(b byte) [AnchorLength]byte {
	var a [AnchorLength]byte
	for i := range a {
		a[i] = b
	}

	return a
}

// TestComputeTokenIDMatchesABIEncode independently reproduces keccak256(abi.encode(anchor, index))
// and asserts ComputeTokenID matches it. This validates the abi.encode replication the Solidity
// computeTokenID relies on.
func TestComputeTokenIDMatchesABIEncode(t *testing.T) {
	a := anchor(0xAB)
	const index = 5

	// abi.encode(bytes32, uint256): 32-byte anchor followed by the 32-byte big-endian index.
	buf := make([]byte, 64)
	copy(buf[:32], a[:])
	buf[63] = index // low byte of the big-endian uint256

	assert.Equal(t, crypto.Keccak256Hash(buf), ComputeTokenID(a, index))
}

// TestGoldenVectors locks the derivations so they cannot drift; the Solidity side reproduces these
// exact values in Phase 2.
func TestGoldenVectors(t *testing.T) {
	tokenID := ComputeTokenID(anchor(0x11), 0)
	assert.Equal(t, "5c75bb376affa44a4f06c8a768453c2f7945122a65eb322a0dd3cc2edcbd6f0a", hex.EncodeToString(tokenID[:]))

	transferMeta := TransferMetadataKey("recipient")
	assert.Equal(t, "6699c8fc9ad0bd918c66a60bc47fbf51cc03d6ea228e1a47415933a087732c3e", hex.EncodeToString(transferMeta[:]))

	serial := SpentRefForSerial([]byte("serial-1"))
	assert.Equal(t, "9bee014234d7610ed8a97979094d359a29999a47ce421601ae5c406eac68d3b5", hex.EncodeToString(serial[:]))

	snMarker := OutputSNMarker(anchor(0x11), 0, []byte("token-bytes"))
	assert.Equal(t, "5d8bb27168582d235e93ba5a0ba67e00cb1be9b4ff01c817d5a78f2dc334ca52", hex.EncodeToString(snMarker[:]))
}

// TestOutputSNMarkerBinding guards the security property: the marker binds the token content, so a
// different content (or index/anchor) at the same position yields a different marker, and it is
// distinct from the addressable token id.
func TestOutputSNMarkerBinding(t *testing.T) {
	a := anchor(0x11)
	base := OutputSNMarker(a, 0, []byte("real-token"))

	assert.Equal(t, base, OutputSNMarker(a, 0, []byte("real-token")), "deterministic")
	assert.NotEqual(t, base, OutputSNMarker(a, 0, []byte("forged-token")), "different content must not match")
	assert.NotEqual(t, base, OutputSNMarker(a, 1, []byte("real-token")), "different index must differ")
	assert.NotEqual(t, base, OutputSNMarker(anchor(0x12), 0, []byte("real-token")), "different anchor must differ")
	assert.NotEqual(t, base, ComputeTokenID(a, 0), "marker must not collide with the addressable id")
}

func TestComputeTokenIDDistinct(t *testing.T) {
	a, b := anchor(0x01), anchor(0x02)
	// different index or anchor must yield different ids (length-safety / no collisions)
	assert.NotEqual(t, ComputeTokenID(a, 0), ComputeTokenID(a, 1))
	assert.NotEqual(t, ComputeTokenID(a, 0), ComputeTokenID(b, 0))
	assert.NotEqual(t, ComputeTokenID(a, 255), ComputeTokenID(a, 256))
}

// TestDomainSeparation guards that the different key classes never collide for the same input.
func TestDomainSeparation(t *testing.T) {
	assert.NotEqual(t, IssueMetadataKey("k"), TransferMetadataKey("k"))
	assert.NotEqual(t, TransferMetadataKey("k"), SpentRefForSerial([]byte("k")))
	assert.NotEqual(t, IssueMetadataKey("k"), SpentRefForSerial([]byte("k")))

	assert.NotEqual(t, TransferMetadataKey("a"), TransferMetadataKey("b"))
	assert.NotEqual(t, SpentRefForSerial([]byte("sn1")), SpentRefForSerial([]byte("sn2")))
}

func TestDeterminism(t *testing.T) {
	a := anchor(0x42)
	id1, id2 := ComputeTokenID(a, 7), ComputeTokenID(a, 7)
	assert.Equal(t, id1, id2)
	mk1, mk2 := TransferMetadataKey("x"), TransferMetadataKey("x")
	assert.Equal(t, mk1, mk2)
	sr1, sr2 := SpentRefForSerial([]byte("s")), SpentRefForSerial([]byte("s"))
	assert.Equal(t, sr1, sr2)
}

func TestAnchorFromTxID(t *testing.T) {
	a := anchor(0x11)
	got, err := AnchorFromTxID(hex.EncodeToString(a[:]))
	require.NoError(t, err)
	assert.Equal(t, a, got)

	_, err = AnchorFromTxID("zzzz")
	require.Error(t, err, "non-hex must be rejected")
	_, err = AnchorFromTxID("1234")
	assert.Error(t, err, "wrong length must be rejected")
}

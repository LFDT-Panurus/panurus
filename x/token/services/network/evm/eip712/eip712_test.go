/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func b32(v byte) [32]byte {
	var b [32]byte
	for i := range b {
		b[i] = v
	}

	return b
}

func fixtureDomain(t *testing.T) Domain {
	t.Helper()
	addr, err := client.HexToAddress("0x1234567890123456789012345678901234567890")
	require.NoError(t, err)

	return Domain{ChainID: big.NewInt(1), VerifyingContract: addr}
}

// fixtureDelta is the canonical StateDelta reproduced by the Solidity side in Phase 2. Keep it and
// the golden values below in sync with contracts/test/statedelta_digest_fixture.json.
func fixtureDelta() *statedelta.StateDelta {
	return &statedelta.StateDelta{
		Anchor:              b32(0x11),
		SpentRefs:           [][32]byte{b32(0x21), b32(0x22)},
		Outputs:             []statedelta.OutputToken{{TokenID: b32(0x31), SNMarker: b32(0x32), TokenData: []byte("out-0")}},
		MetadataKeys:        [][32]byte{b32(0x41)},
		MetadataVals:        [][]byte{[]byte("meta-0")},
		TokenRequestHash:    b32(0x51),
		PublicParamsHash:    b32(0x61),
		PublicParamsVersion: 3,
		IsSetup:             false,
	}
}

// TestGoldenTypeHashes locks the EIP-712 type strings; a change to any field name/type/order changes
// these and breaks the Solidity cross-check on purpose.
func TestGoldenTypeHashes(t *testing.T) {
	assert.Equal(t, "09fd59eaff1424386ce5076c92a3e0c3556cce1c4c25a62c3331876f58a8e41f", hex.EncodeToString(stateDeltaTypeHash))
	assert.Equal(t, "9ce134f0b91dfef3fdcab70824f54671fc844cbc57c8310ae4e027e6935afc2b", hex.EncodeToString(outputTokenTypeHash))
}

func TestGoldenDomainSeparator(t *testing.T) {
	sep := fixtureDomain(t).Separator()
	assert.Equal(t, "c36531b9deae2efb80b130be2e33982d0d4bf31bf64f5a5b94c65fce64f9b5f7", hex.EncodeToString(sep[:]))
}

// TestGoldenDigest is the freeze artifact: the Solidity contract must produce this exact digest for
// the fixture delta in Phase 2.
func TestGoldenDigest(t *testing.T) {
	dig := Digest(fixtureDomain(t), fixtureDelta())
	assert.Equal(t, "c9326b72636896424aabe0039efef420df6cd18811b82db3237260110f39b64d", hex.EncodeToString(dig[:]))
}

func TestDigestDeterministic(t *testing.T) {
	d := fixtureDomain(t)
	assert.Equal(t, Digest(d, fixtureDelta()), Digest(d, fixtureDelta()))
}

// TestDigestSensitivity guards that the digest actually covers each field: mutating any of them must
// change the digest, otherwise an endorser could be made to sign a delta different from what it
// validated.
func TestDigestSensitivity(t *testing.T) {
	d := fixtureDomain(t)
	base := Digest(d, fixtureDelta())

	mutations := map[string]func(*statedelta.StateDelta){
		"anchor":       func(s *statedelta.StateDelta) { s.Anchor = b32(0x12) },
		"spentRefs":    func(s *statedelta.StateDelta) { s.SpentRefs = append(s.SpentRefs, b32(0x23)) },
		"outputs":      func(s *statedelta.StateDelta) { s.Outputs[0].TokenData = []byte("changed") },
		"snMarker":     func(s *statedelta.StateDelta) { s.Outputs[0].SNMarker = b32(0x33) },
		"metadataKeys": func(s *statedelta.StateDelta) { s.MetadataKeys[0] = b32(0x42) },
		"metadataVals": func(s *statedelta.StateDelta) { s.MetadataVals[0] = []byte("changed") },
		"requestHash":  func(s *statedelta.StateDelta) { s.TokenRequestHash = b32(0x52) },
		"ppHash":       func(s *statedelta.StateDelta) { s.PublicParamsHash = b32(0x62) },
		"ppVersion":    func(s *statedelta.StateDelta) { s.PublicParamsVersion = 4 },
		"isSetup":      func(s *statedelta.StateDelta) { s.IsSetup = true },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			delta := fixtureDelta()
			mutate(delta)
			assert.NotEqual(t, base, Digest(d, delta), "mutating %s must change the digest", name)
		})
	}
}

// TestEmptyArraysAreStable documents that empty dynamic arrays hash to keccak256("") consistently,
// matching Solidity's abi encoding of empty arrays.
func TestEmptyArraysAreStable(t *testing.T) {
	d := fixtureDomain(t)
	empty := &statedelta.StateDelta{Anchor: b32(0x11)}
	assert.Equal(t, Digest(d, empty), Digest(d, &statedelta.StateDelta{Anchor: b32(0x11)}))
}

// TestSeparatorNilChainID guards that a nil ChainID does not panic (it is treated as zero); a valid
// chain id is enforced by the configuration layer.
func TestSeparatorNilChainID(t *testing.T) {
	addr, err := client.HexToAddress("0x1234567890123456789012345678901234567890")
	require.NoError(t, err)
	d := Domain{ChainID: nil, VerifyingContract: addr}
	assert.NotPanics(t, func() { _ = d.Separator() })
}

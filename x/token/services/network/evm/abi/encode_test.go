/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package abi

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

func word(v byte) [32]byte {
	var a [32]byte
	a[31] = v

	return a
}

// goldenDelta is the delta pinned by TestEncodeApplyStateDeltaGoldenVector. It deliberately covers
// the shapes that make the head/tail layout non-trivial: several dynamic arrays, a struct array whose
// elements carry dynamic bytes, and an output whose data spans more than one word (so the tail
// padding matters).
func goldenDelta() *statedelta.StateDelta {
	return &statedelta.StateDelta{
		Anchor:    word(0xA1),
		SpentRefs: [][32]byte{word(0x11), word(0x22)},
		Outputs: []statedelta.OutputToken{
			{TokenID: word(0x31), SNMarker: word(0x32), TokenData: []byte("out-0")},
			{TokenID: word(0x41), SNMarker: word(0x42), TokenData: []byte("second-output-longer-than-one-word-to-test-padding")},
		},
		MetadataKeys:        [][32]byte{word(0x51)},
		MetadataVals:        [][]byte{[]byte("meta-0")},
		TokenRequestHash:    word(0x61),
		PublicParamsHash:    word(0x71),
		PublicParamsVersion: 3,
	}
}

func goldenSignatures() [][]byte {
	a := make([]byte, 65)
	a[64] = 27
	b := make([]byte, 65)
	b[64] = 28

	return [][]byte{a, b}
}

// TestApplyStateDeltaSelector pins the selector against the compiled contract ABI. The canonical
// signature must match what the contract dispatches on, or every call reverts as an unknown method.
func TestApplyStateDeltaSelector(t *testing.T) {
	// Verified with `cast sig` against the signature taken from the compiled TokenState ABI.
	assert.Equal(t, "e7920565", hex.EncodeToString(MethodID(ApplyStateDeltaMethod)))
}

// TestEncodeApplyStateDeltaGoldenVector is the Phase-B gate: the calldata must match, byte for byte,
// a vector independently decoded with foundry's `cast decode-calldata`, which round-tripped every
// field (anchor, both spent refs, both outputs including the multi-word one, metadata, hashes,
// version, isSetup, and both signatures). That pins the ABI head/tail layout for nested dynamic
// types, which is the part most easily got wrong.
func TestEncodeApplyStateDeltaGoldenVector(t *testing.T) {
	want :=
		"e792056500000000000000000000000000000000000000000000000000000000" +
			"0000004000000000000000000000000000000000000000000000000000000000" +
			"0000048000000000000000000000000000000000000000000000000000000000" +
			"000000a100000000000000000000000000000000000000000000000000000000" +
			"0000014000000000000000000000000000000000000000000000000000000000" +
			"000001a000000000000000000000000000000000000000000000000000000000" +
			"0000036000000000000000000000000000000000000000000000000000000000" +
			"000003a000000000000000000000000000000000000000000000000000000000" +
			"0000006100000000000000000000000000000000000000000000000000000000" +
			"0000007100000000000000000000000000000000000000000000000000000000" +
			"0000000300000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000042000000000000000000000000000000000000000000000000000000000" +
			"0000000200000000000000000000000000000000000000000000000000000000" +
			"0000001100000000000000000000000000000000000000000000000000000000" +
			"0000002200000000000000000000000000000000000000000000000000000000" +
			"0000000200000000000000000000000000000000000000000000000000000000" +
			"0000004000000000000000000000000000000000000000000000000000000000" +
			"000000e000000000000000000000000000000000000000000000000000000000" +
			"0000003100000000000000000000000000000000000000000000000000000000" +
			"0000003200000000000000000000000000000000000000000000000000000000" +
			"0000006000000000000000000000000000000000000000000000000000000000" +
			"000000056f75742d300000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000004100000000000000000000000000000000000000000000000000000000" +
			"0000004200000000000000000000000000000000000000000000000000000000" +
			"0000006000000000000000000000000000000000000000000000000000000000" +
			"000000327365636f6e642d6f75747075742d6c6f6e6765722d7468616e2d6f6e" +
			"652d776f72642d746f2d746573742d70616464696e6700000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000000100000000000000000000000000000000000000000000000000000000" +
			"0000005100000000000000000000000000000000000000000000000000000000" +
			"0000000100000000000000000000000000000000000000000000000000000000" +
			"0000002000000000000000000000000000000000000000000000000000000000" +
			"000000066d6574612d3000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000000200000000000000000000000000000000000000000000000000000000" +
			"0000004000000000000000000000000000000000000000000000000000000000" +
			"000000c000000000000000000000000000000000000000000000000000000000" +
			"0000004100000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"000000001b000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"0000004100000000000000000000000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000000" +
			"000000001c000000000000000000000000000000000000000000000000000000" +
			"00000000"

	got := EncodeApplyStateDelta(goldenDelta(), goldenSignatures())
	assert.Equal(t, want, hex.EncodeToString(got))
}

// TestEncodeApplyStateDeltaSetupShape covers the other delta the driver sends: a public-parameters
// update, with empty arrays and non-empty setup parameters.
func TestEncodeApplyStateDeltaSetupShape(t *testing.T) {
	delta := &statedelta.StateDelta{
		Anchor:              word(0x77),
		TokenRequestHash:    word(0x88),
		PublicParamsHash:    word(0x99),
		PublicParamsVersion: 4,
		IsSetup:             true,
		SetupParameters:     []byte("pp-v4"),
	}
	got := EncodeApplyStateDelta(delta, goldenSignatures())

	require.Greater(t, len(got), selectorLength)
	assert.Equal(t, MethodID(ApplyStateDeltaMethod), got[:selectorLength])
	// the encoding is word aligned after the selector
	assert.Zero(t, (len(got)-selectorLength)%wordLength)
}

// TestEncodeIsDeterministic checks the encoder has no map iteration or other nondeterminism: the
// same delta must always produce the same calldata, since the endorsers signed those exact bytes.
func TestEncodeIsDeterministic(t *testing.T) {
	first := EncodeApplyStateDelta(goldenDelta(), goldenSignatures())
	for range 5 {
		assert.Equal(t, first, EncodeApplyStateDelta(goldenDelta(), goldenSignatures()))
	}
}

// TestEncodeFieldsAreCovered checks each delta field actually reaches the calldata: changing any one
// of them changes the encoding.
func TestEncodeFieldsAreCovered(t *testing.T) {
	base := hex.EncodeToString(EncodeApplyStateDelta(goldenDelta(), goldenSignatures()))

	mutations := map[string]func(*statedelta.StateDelta){
		"anchor":       func(d *statedelta.StateDelta) { d.Anchor = word(0xFF) },
		"spent refs":   func(d *statedelta.StateDelta) { d.SpentRefs = [][32]byte{word(0xFF)} },
		"outputs":      func(d *statedelta.StateDelta) { d.Outputs = nil },
		"output data":  func(d *statedelta.StateDelta) { d.Outputs[0].TokenData = []byte("changed") },
		"metadata key": func(d *statedelta.StateDelta) { d.MetadataKeys = [][32]byte{word(0xFF)} },
		"metadata val": func(d *statedelta.StateDelta) { d.MetadataVals = [][]byte{[]byte("changed")} },
		"tr hash":      func(d *statedelta.StateDelta) { d.TokenRequestHash = word(0xFF) },
		"pp hash":      func(d *statedelta.StateDelta) { d.PublicParamsHash = word(0xFF) },
		"pp version":   func(d *statedelta.StateDelta) { d.PublicParamsVersion = 99 },
		"is setup":     func(d *statedelta.StateDelta) { d.IsSetup = true },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			d := goldenDelta()
			mutate(d)
			assert.NotEqual(t, base, hex.EncodeToString(EncodeApplyStateDelta(d, goldenSignatures())),
				"%s must be covered by the encoding", name)
		})
	}
}

// TestEncodeSignaturesAreCovered checks the signature bundle reaches the calldata.
func TestEncodeSignaturesAreCovered(t *testing.T) {
	base := EncodeApplyStateDelta(goldenDelta(), goldenSignatures())
	fewer := EncodeApplyStateDelta(goldenDelta(), goldenSignatures()[:1])
	assert.NotEqual(t, base, fewer)

	none := EncodeApplyStateDelta(goldenDelta(), nil)
	assert.NotEqual(t, base, none)
}

// --- value encoder unit tests --------------------------------------------------------------------

func TestStaticValues(t *testing.T) {
	assert.Equal(t, "00000000000000000000000000000000000000000000000000000000000000ff",
		hex.EncodeToString(Uint64(255).encode()))
	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000000001",
		hex.EncodeToString(Bool(true).encode()))
	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000000000",
		hex.EncodeToString(Bool(false).encode()))
	assert.Equal(t, "00000000000000000000000000000000000000000000000000000000000004d2",
		hex.EncodeToString(Uint256(big.NewInt(1234)).encode()))
	assert.Equal(t, "0000000000000000000000000000000000000000000000000000000000000000",
		hex.EncodeToString(Uint256(nil).encode()), "a nil big.Int encodes as zero")

	for _, v := range []Value{Uint64(1), Bool(true), Uint256(big.NewInt(1)), Bytes32(word(1))} {
		assert.False(t, v.isDynamic())
	}
}

// TestBytesPadding checks dynamic bytes are length prefixed and right padded to a word boundary.
func TestBytesPadding(t *testing.T) {
	cases := map[string]int{
		"":                                 32,      // just the length word
		"a":                                32 + 32, // length + one padded word
		"exactly thirty two bytes of data": 32 + 32, // 32 bytes fills one word exactly, no padding
		"this payload is definitely longer than thirty two bytes": 32 + 64,
	}
	for in, wantLen := range cases {
		require.Len(t, []byte("exactly thirty two bytes of data"), 32, "fixture must really be 32 bytes")
		assert.Len(t, Bytes([]byte(in)).encode(), wantLen, "payload %q (%d bytes)", in, len(in))
	}
}

// TestTupleDynamicity checks a tuple is dynamic exactly when one of its members is, which decides
// whether it is inlined or referenced by offset.
func TestTupleDynamicity(t *testing.T) {
	assert.False(t, Tuple(Bytes32(word(1)), Uint64(2)).isDynamic(), "all static members")
	assert.True(t, Tuple(Bytes32(word(1)), Bytes([]byte("x"))).isDynamic(), "one dynamic member")
	assert.True(t, Array().isDynamic(), "arrays are always dynamic")
	assert.True(t, Bytes(nil).isDynamic(), "bytes are always dynamic")
}

// TestEmptyArraysEncodeAsLengthZero checks the empty cases the setup delta relies on.
func TestEmptyArraysEncodeAsLengthZero(t *testing.T) {
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	assert.Equal(t, zero, hex.EncodeToString(Bytes32Array(nil).encode()))
	assert.Equal(t, zero, hex.EncodeToString(BytesArray(nil).encode()))
	assert.Equal(t, zero, hex.EncodeToString(Array().encode()))
}

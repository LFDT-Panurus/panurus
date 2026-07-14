/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// EIP-712 type strings. Per the spec, encodeType lists the primary type first, followed by the
// referenced structs in alphabetical order (here only OutputToken). The field order must match the
// Solidity struct, the Go struct, and encodeData below, byte-for-byte.
const (
	outputTokenType = "OutputToken(bytes32 tokenID,bytes32 snMarker,bytes tokenData)" // #nosec G101 -- EIP-712 type string, not a credential
	stateDeltaType  = "StateDelta(bytes32 anchor,bytes32[] spentRefs,OutputToken[] outputs,bytes32[] metadataKeys,bytes[] metadataVals,bytes32 tokenRequestHash,bytes32 publicParamsHash,uint64 publicParamsVersion,bool isSetup,bytes setupParameters)" + outputTokenType
)

var (
	outputTokenTypeHash = crypto.Keccak256([]byte(outputTokenType))
	stateDeltaTypeHash  = crypto.Keccak256([]byte(stateDeltaType))
)

// HashStruct returns the EIP-712 hashStruct(StateDelta) = keccak256(typeHash || encodeData(delta)).
//
// encodeData encodes each member as a 32-byte word: atomic values (bytes32, uint64, bool) directly;
// dynamic bytes as keccak256(value); arrays as keccak256 of their concatenated element encodings; and
// the OutputToken struct array as keccak256 of the concatenated hashStruct of each element.
func HashStruct(d *statedelta.StateDelta) [32]byte {
	buf := make([]byte, 0, 11*wordLen)
	buf = append(buf, stateDeltaTypeHash...)
	buf = append(buf, d.Anchor[:]...)
	buf = append(buf, hashBytes32Array(d.SpentRefs)...)
	buf = append(buf, hashOutputTokenArray(d.Outputs)...)
	buf = append(buf, hashBytes32Array(d.MetadataKeys)...)
	buf = append(buf, hashBytesArray(d.MetadataVals)...)
	buf = append(buf, d.TokenRequestHash[:]...)
	buf = append(buf, d.PublicParamsHash[:]...)
	buf = append(buf, uint64Word(d.PublicParamsVersion)...)
	buf = append(buf, boolWord(d.IsSetup)...)
	buf = append(buf, crypto.Keccak256(d.SetupParameters)...)

	return crypto.Keccak256Hash(buf)
}

// hashBytes32Array encodes a bytes32[] as keccak256 of the concatenated 32-byte elements.
func hashBytes32Array(arr [][32]byte) []byte {
	buf := make([]byte, 0, len(arr)*wordLen)
	for i := range arr {
		buf = append(buf, arr[i][:]...)
	}

	return crypto.Keccak256(buf)
}

// hashBytesArray encodes a bytes[] as keccak256 of the concatenated keccak256 of each element.
func hashBytesArray(arr [][]byte) []byte {
	buf := make([]byte, 0, len(arr)*wordLen)
	for _, e := range arr {
		buf = append(buf, crypto.Keccak256(e)...)
	}

	return crypto.Keccak256(buf)
}

// hashOutputTokenArray encodes an OutputToken[] as keccak256 of the concatenated hashStruct of each element.
func hashOutputTokenArray(arr []statedelta.OutputToken) []byte {
	buf := make([]byte, 0, len(arr)*wordLen)
	for _, o := range arr {
		buf = append(buf, hashOutputToken(o)...)
	}

	return crypto.Keccak256(buf)
}

// hashOutputToken returns hashStruct(OutputToken) =
// keccak256(typeHash || tokenID || snMarker || keccak256(tokenData)).
func hashOutputToken(o statedelta.OutputToken) []byte {
	buf := make([]byte, 0, 4*wordLen)
	buf = append(buf, outputTokenTypeHash...)
	buf = append(buf, o.TokenID[:]...)
	buf = append(buf, o.SNMarker[:]...)
	buf = append(buf, crypto.Keccak256(o.TokenData)...)

	return crypto.Keccak256(buf)
}

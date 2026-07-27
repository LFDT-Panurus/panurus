/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package abi is a minimal, dependency-free ABI codec for the read-only contract calls the driver
// makes: the 4-byte method selector, the encoding of a call taking a single bytes32 argument, and
// the decoding of the two return shapes the TokenState reads use, a dynamic `bytes` and a `uint64`.
//
// It exists so the endorser's validation ledger can read on-chain state (getToken, getPublicParameters,
// getPublicParamsVersion) without go-ethereum, whose license is a hard blocker for the project (design
// §9, §15.5). It deliberately covers only the static-argument reads the driver needs; general ABI
// (tuples, dynamic arguments, arrays) is not implemented because nothing here calls for it. The
// write-side encoding of applyStateDelta's typed tuple lands with the driver in Week 5.
package abi

import (
	"encoding/binary"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// wordLength is the size in bytes of an ABI word (a 256-bit slot). Every static value and every
// head/tail offset is encoded in one word.
const wordLength = 32

// selectorLength is the size in bytes of a function selector: the first four bytes of the Keccak-256
// of the canonical function signature.
const selectorLength = 4

// MethodID returns the 4-byte function selector for the canonical signature, e.g.
// "getToken(bytes32)". The signature must be the canonical form the ABI spec hashes: no argument
// names, no spaces, canonical type names (uint256 not uint).
func MethodID(signature string) []byte {
	h := crypto.Keccak256([]byte(signature))

	return h[:selectorLength]
}

// EncodeBytes32Call encodes a call to a method taking a single bytes32 argument: the selector
// followed by the 32-byte argument, which for a bytes32 is stored as-is (left-aligned, no padding).
func EncodeBytes32Call(signature string, arg [32]byte) []byte {
	out := make([]byte, 0, selectorLength+wordLength)
	out = append(out, MethodID(signature)...)
	out = append(out, arg[:]...)

	return out
}

// DecodeBytes decodes the ABI encoding of a single dynamic `bytes` (or `string`) return value: one
// head word holding the offset to the tail, then at the tail one word holding the length, then the
// bytes themselves. It validates every bound so a short or inconsistent response is an error rather
// than a panic or a silently truncated value.
func DecodeBytes(ret []byte) ([]byte, error) {
	if len(ret) < wordLength {
		return nil, errors.Errorf("abi: bytes return too short for offset word: %d bytes", len(ret))
	}
	offset, err := readWordAsLength(ret[:wordLength], "offset")
	if err != nil {
		return nil, err
	}
	if offset > len(ret)-wordLength {
		return nil, errors.Errorf("abi: bytes offset %d out of bounds (len %d)", offset, len(ret))
	}
	lengthWord := ret[offset : offset+wordLength]
	length, err := readWordAsLength(lengthWord, "length")
	if err != nil {
		return nil, err
	}
	dataStart := offset + wordLength
	if length > len(ret)-dataStart {
		return nil, errors.Errorf("abi: bytes length %d out of bounds (offset %d, len %d)", length, offset, len(ret))
	}

	out := make([]byte, length)
	copy(out, ret[dataStart:dataStart+length])

	return out, nil
}

// DecodeUint64 decodes a uint64 return value: one word holding the value right-aligned (big-endian).
// The high 24 bytes must be zero, so a value that does not fit in 64 bits is an error rather than a
// silent truncation.
func DecodeUint64(ret []byte) (uint64, error) {
	if len(ret) < wordLength {
		return 0, errors.Errorf("abi: uint64 return too short: %d bytes", len(ret))
	}
	for _, b := range ret[:wordLength-8] {
		if b != 0 {
			return 0, errors.Errorf("abi: value does not fit in uint64 (high bytes set)")
		}
	}

	return binary.BigEndian.Uint64(ret[wordLength-8 : wordLength]), nil
}

// readWordAsLength reads a 32-byte word as a non-negative length that fits in a platform int. ABI
// offsets and lengths are 256-bit but any real response is far below the int range; a word above it
// is a malformed response, not a value to accommodate.
func readWordAsLength(word []byte, what string) (int, error) {
	if len(word) < wordLength {
		return 0, errors.Errorf("abi: %s word too short: %d bytes", what, len(word))
	}
	for _, b := range word[:wordLength-8] {
		if b != 0 {
			return 0, errors.Errorf("abi: %s word too large to be a valid length", what)
		}
	}
	v := binary.BigEndian.Uint64(word[wordLength-8 : wordLength])
	if v > uint64(maxInt) {
		return 0, errors.Errorf("abi: %s %d exceeds max int", what, v)
	}

	return int(v), nil
}

// maxInt is the largest value of the platform int, used to bound-check ABI lengths before conversion.
const maxInt = int(^uint(0) >> 1)

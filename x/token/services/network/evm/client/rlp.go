/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"math/big"
)

// Minimal RLP encoder, enough for EIP-1559 (type-2) transaction encoding. RLP is only needed on the
// write path (building the raw transaction to broadcast); nothing in the driver decodes RLP, so this
// is deliberately encode-only.
//
// It is hand-rolled because go-ethereum's license is a hard blocker for the project (design §9,
// §15.6). The encoding follows the Ethereum yellow paper: a byte string under 56 bytes is prefixed
// with 0x80+len, longer strings with 0xb7+lenOfLen followed by the big-endian length; lists use
// 0xc0/0xf7 the same way. A single byte below 0x80 encodes as itself.

const (
	// rlpStringShortBase is the prefix for a byte string shorter than 56 bytes.
	rlpStringShortBase = 0x80
	// rlpStringLongBase is the prefix for a byte string of 56 bytes or more.
	rlpStringLongBase = 0xb7
	// rlpListShortBase is the prefix for a list whose payload is shorter than 56 bytes.
	rlpListShortBase = 0xc0
	// rlpListLongBase is the prefix for a list whose payload is 56 bytes or more.
	rlpListLongBase = 0xf7
	// rlpShortLimit is the payload-length boundary between the short and long forms.
	rlpShortLimit = 56
)

// rlpString encodes a byte string.
func rlpString(b []byte) []byte {
	// A single byte in [0x00, 0x7f] is its own encoding.
	if len(b) == 1 && b[0] < rlpStringShortBase {
		return []byte{b[0]}
	}

	return append(rlpLengthPrefix(len(b), rlpStringShortBase, rlpStringLongBase), b...)
}

// rlpList encodes an already-encoded concatenation of items as a list.
func rlpList(payload []byte) []byte {
	return append(rlpLengthPrefix(len(payload), rlpListShortBase, rlpListLongBase), payload...)
}

// rlpUint encodes an integer as a minimal big-endian byte string: zero encodes as the empty string
// (0x80), and leading zero bytes are never emitted. This canonical form is what the signature and
// the transaction hash are computed over, so a non-minimal encoding would produce a different hash
// and a transaction the node rejects.
func rlpUint(x uint64) []byte {
	return rlpString(minimalBytes(x))
}

// rlpBigInt encodes a non-negative big.Int as a minimal big-endian byte string. A nil or zero value
// encodes as the empty string, matching rlpUint(0).
func rlpBigInt(x *big.Int) []byte {
	if x == nil || x.Sign() == 0 {
		return rlpString(nil)
	}

	return rlpString(x.Bytes())
}

// rlpLengthPrefix returns the length prefix for a payload of the given size, using shortBase for
// payloads under 56 bytes and longBase (plus the big-endian length) for larger ones.
func rlpLengthPrefix(length int, shortBase, longBase byte) []byte {
	if length < rlpShortLimit {
		return []byte{shortBase + byte(length)} // #nosec G115 -- length < 56, fits in a byte
	}
	lenBytes := minimalBytes(uint64(length)) // #nosec G115 -- length is non-negative
	out := make([]byte, 0, 1+len(lenBytes))
	out = append(out, longBase+byte(len(lenBytes))) // #nosec G115 -- at most 8 length bytes

	return append(out, lenBytes...)
}

// minimalBytes returns x as big-endian bytes with no leading zeros; zero returns an empty slice.
func minimalBytes(x uint64) []byte {
	if x == 0 {
		return nil
	}
	var buf [8]byte
	i := len(buf)
	for x > 0 {
		i--
		buf[i] = byte(x) // #nosec G115 -- truncation to the low byte is intended
		x >>= 8
	}

	return buf[i:]
}

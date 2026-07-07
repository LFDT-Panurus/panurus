/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"encoding/binary"
	"math/big"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// wordLen is the size in bytes of an ABI / EIP-712 encoded word.
const wordLen = 32

// uint64Word encodes a uint64 as a big-endian, left-padded 32-byte word. EIP-712 encodes all integer
// types (here uint64) as a 32-byte word regardless of their declared width.
func uint64Word(x uint64) []byte {
	w := make([]byte, wordLen)
	binary.BigEndian.PutUint64(w[wordLen-8:], x)

	return w
}

// bigWord encodes a non-negative big.Int as a big-endian, left-padded 32-byte word.
func bigWord(x *big.Int) []byte {
	return leftPad(x.Bytes())
}

// boolWord encodes a bool as a 32-byte word (0 or 1).
func boolWord(b bool) []byte {
	w := make([]byte, wordLen)
	if b {
		w[wordLen-1] = 1
	}

	return w
}

// addressWord encodes a 20-byte address into the low-order 20 bytes of a 32-byte word.
func addressWord(a client.Address) []byte {
	w := make([]byte, wordLen)
	copy(w[wordLen-client.AddressLength:], a.Bytes())

	return w
}

// leftPad returns b as a 32-byte word, left-padded with zeros (or low-order truncated if longer).
func leftPad(b []byte) []byte {
	if len(b) >= wordLen {
		return b[len(b)-wordLen:]
	}
	w := make([]byte, wordLen)
	copy(w[wordLen-len(b):], b)

	return w
}

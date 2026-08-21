/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package abi

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// abiEncodedBytes builds a well-formed `bytes` return value: an offset word, a length word, then the
// payload padded to a whole number of words.
func abiEncodedBytes(payload []byte) []byte {
	head := make([]byte, wordLength)
	head[wordLength-1] = 0x20
	length := make([]byte, wordLength)
	binary.BigEndian.PutUint64(length[wordLength-8:], uint64(len(payload)))
	padded := make([]byte, (len(payload)+wordLength-1)/wordLength*wordLength)
	copy(padded, payload)

	return append(append(head, length...), padded...)
}

// FuzzDecodeBytes fuzzes the dynamic-bytes decoder.
//
// Every one of these decoders reads a response an EVM node handed back. The driver reaches for a node
// it was configured with, but a compromised, buggy or simply mismatched node can return anything, and
// the offset and length words are attacker-controlled integers used to slice a buffer. That is the
// classic shape for an out-of-bounds panic, so the property is that a malformed response is an error
// and never a panic or a silently truncated value.
func FuzzDecodeBytes(f *testing.F) {
	f.Add(abiEncodedBytes([]byte("public parameters")))
	f.Add(abiEncodedBytes(nil))
	f.Add([]byte{})
	f.Add(make([]byte, wordLength))       // an offset word alone, pointing nowhere
	f.Add(make([]byte, wordLength-1))     // shorter than one word
	f.Add(bytes.Repeat([]byte{0xff}, 64)) // offset and length both enormous

	f.Fuzz(func(t *testing.T, ret []byte) {
		out, err := DecodeBytes(ret)
		if err != nil {
			return
		}
		// Anything accepted must actually have been present: a decoder that returns more bytes than it
		// was given has read past its input.
		if len(out) > len(ret) {
			t.Fatalf("decoded %d bytes from a %d byte response", len(out), len(ret))
		}
	})
}

// FuzzDecodeBoolArray fuzzes the bool[] decoder, which is how the driver reads areTokensSpent. It has
// the same offset-and-length shape as DecodeBytes plus a per-element loop, so a length word that
// disagrees with the buffer is the failure to look for.
func FuzzDecodeBoolArray(f *testing.F) {
	valid := make([]byte, 0, wordLength*4)
	head := make([]byte, wordLength)
	head[wordLength-1] = 0x20
	length := make([]byte, wordLength)
	length[wordLength-1] = 0x02
	elem := make([]byte, wordLength)
	elem[wordLength-1] = 0x01
	valid = append(valid, head...)
	valid = append(valid, length...)
	valid = append(valid, elem...)
	valid = append(valid, make([]byte, wordLength)...)

	f.Add(valid)
	f.Add([]byte{})
	f.Add(make([]byte, wordLength))
	f.Add(valid[:len(valid)-1])           // truncated final element
	f.Add(bytes.Repeat([]byte{0xff}, 96)) // a length that cannot be satisfied

	f.Fuzz(func(t *testing.T, ret []byte) {
		out, err := DecodeBoolArray(ret)
		if err != nil {
			return
		}
		// One word per element, so a response cannot describe more elements than it has room for.
		if len(out)*wordLength > len(ret) {
			t.Fatalf("decoded %d elements from a %d byte response", len(out), len(ret))
		}
	})
}

// FuzzDecodeUint64 fuzzes the uint64 decoder, which reads the public-parameters version. A word is
// 32 bytes and a uint64 is 8, so the interesting case is a value that does not fit: it must be
// rejected rather than silently truncated, since a wrong version makes a delta stale on chain.
func FuzzDecodeUint64(f *testing.F) {
	valid := make([]byte, wordLength)
	valid[wordLength-1] = 0x07
	overflow := bytes.Repeat([]byte{0xff}, wordLength)

	f.Add(valid)
	f.Add(overflow)
	f.Add([]byte{})
	f.Add(make([]byte, wordLength-1))
	f.Add(make([]byte, wordLength*2))

	f.Fuzz(func(t *testing.T, ret []byte) {
		got, err := DecodeUint64(ret)
		if err != nil {
			return
		}
		if len(ret) < wordLength {
			t.Fatalf("decoded a uint64 from a %d byte response, shorter than one word", len(ret))
		}
		// The value must actually fit: every byte outside the low 8 has to be zero, checked here by an
		// independent comparison rather than the same byte-loop DecodeUint64 itself uses.
		if !bytes.Equal(ret[:wordLength-8], make([]byte, wordLength-8)) {
			t.Fatalf("accepted a value whose high bytes are not zero: it does not fit in a uint64")
		}
		if want := binary.BigEndian.Uint64(ret[wordLength-8 : wordLength]); got != want {
			t.Fatalf("decoded %d, but the low 8 bytes actually encode %d", got, want)
		}
	})
}

// FuzzDecodeBytes32 fuzzes the fixed-width decoder. It cannot fail on content, only on length, so the
// property is simply that a short response never reads past the end.
func FuzzDecodeBytes32(f *testing.F) {
	f.Add(make([]byte, wordLength))
	f.Add([]byte{})
	f.Add(make([]byte, wordLength-1))
	f.Add(bytes.Repeat([]byte{0xab}, wordLength*3))

	f.Fuzz(func(t *testing.T, ret []byte) {
		_, _ = DecodeBytes32(ret)
	})
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package abi

import (
	"encoding/binary"
	"math/big"
)

// The write side of the codec: enough ABI encoding to call applyStateDelta, whose argument is a
// struct with nested dynamic members (bytes32[], a struct array carrying bytes, bytes[]) plus a
// second bytes[] argument. That is the only write the driver makes, but it exercises the trickiest
// part of the ABI: the head/tail layout, where every dynamic value contributes a 32-byte offset in
// the head and its payload in the tail, recursively.
//
// The encoder is expressed as a tiny value model (each value knows whether it is dynamic and how to
// encode its head and tail) so nesting composes correctly instead of being hand-unrolled per shape.

// Value is an ABI-encodable value.
type Value interface {
	// isDynamic reports whether the value is encoded as an offset in the head plus a payload in the
	// tail (dynamic), or inline in the head (static).
	isDynamic() bool
	// encode returns the value's own encoding: the inline word for a static value, the payload for a
	// dynamic one.
	encode() []byte
}

// --- static values -------------------------------------------------------------------------------

type wordValue [32]byte

func (wordValue) isDynamic() bool { return false }

func (v wordValue) encode() []byte {
	out := make([]byte, wordLength)
	copy(out, v[:])

	return out
}

// Bytes32 encodes a bytes32, stored left-aligned as-is.
func Bytes32(b [32]byte) Value { return wordValue(b) }

// Uint64 encodes a uint64 as a right-aligned 32-byte word (the ABI widens all integers to a word).
func Uint64(x uint64) Value {
	var w wordValue
	binary.BigEndian.PutUint64(w[wordLength-8:], x)

	return w
}

// Uint256 encodes a non-negative big.Int as a right-aligned 32-byte word.
func Uint256(x *big.Int) Value {
	var w wordValue
	if x != nil {
		b := x.Bytes()
		if len(b) > wordLength {
			b = b[len(b)-wordLength:]
		}
		copy(w[wordLength-len(b):], b)
	}

	return w
}

// Bool encodes a bool as a 32-byte word (0 or 1).
func Bool(b bool) Value {
	var w wordValue
	if b {
		w[wordLength-1] = 1
	}

	return w
}

// --- dynamic values ------------------------------------------------------------------------------

type bytesValue []byte

func (bytesValue) isDynamic() bool { return true }

// encode lays out a dynamic bytes payload: a length word followed by the data, right-padded to a
// multiple of 32 bytes.
func (v bytesValue) encode() []byte {
	out := make([]byte, 0, wordLength+len(v)+wordLength)
	out = append(out, lengthWord(len(v))...)
	out = append(out, v...)
	if pad := (wordLength - len(v)%wordLength) % wordLength; pad != 0 {
		out = append(out, make([]byte, pad)...)
	}

	return out
}

// Bytes encodes a dynamic byte string.
func Bytes(b []byte) Value { return bytesValue(b) }

type arrayValue []Value

func (arrayValue) isDynamic() bool { return true }

// encode lays out a dynamic array: a length word followed by the head/tail encoding of its elements.
func (v arrayValue) encode() []byte {
	return append(lengthWord(len(v)), encodeValues(v)...)
}

// Array encodes a dynamic array (T[]) of the given elements.
func Array(elems ...Value) Value { return arrayValue(elems) }

// Bytes32Array encodes a bytes32[].
func Bytes32Array(items [][32]byte) Value {
	elems := make([]Value, len(items))
	for i := range items {
		elems[i] = Bytes32(items[i])
	}

	return arrayValue(elems)
}

// BytesArray encodes a bytes[].
func BytesArray(items [][]byte) Value {
	elems := make([]Value, len(items))
	for i := range items {
		elems[i] = Bytes(items[i])
	}

	return arrayValue(elems)
}

type tupleValue []Value

// isDynamic reports whether the tuple contains any dynamic member; a tuple is dynamic iff one of its
// members is, which is what decides whether it is inlined or referenced by offset.
func (v tupleValue) isDynamic() bool {
	for _, f := range v {
		if f.isDynamic() {
			return true
		}
	}

	return false
}

func (v tupleValue) encode() []byte { return encodeValues(v) }

// Tuple encodes a struct with the given fields, in declaration order.
func Tuple(fields ...Value) Value { return tupleValue(fields) }

// --- layout --------------------------------------------------------------------------------------

// encodeValues lays out a sequence of values in ABI head/tail form: static values inline in the head,
// dynamic values as an offset word in the head (measured from the start of the sequence) with the
// payload appended to the tail.
func encodeValues(values []Value) []byte {
	headSize := 0
	for _, v := range values {
		if v.isDynamic() {
			headSize += wordLength
		} else {
			headSize += len(v.encode())
		}
	}

	head := make([]byte, 0, headSize)
	var tail []byte
	for _, v := range values {
		if v.isDynamic() {
			head = append(head, lengthWord(headSize+len(tail))...)
			tail = append(tail, v.encode()...)

			continue
		}
		head = append(head, v.encode()...)
	}

	return append(head, tail...)
}

// EncodeCall returns the calldata for a method: its 4-byte selector followed by the ABI encoding of
// the arguments. The signature must be the canonical form (see MethodID).
func EncodeCall(signature string, args ...Value) []byte {
	return append(MethodID(signature), encodeValues(args)...)
}

// lengthWord encodes a non-negative length or offset as a 32-byte word.
func lengthWord(n int) []byte {
	w := make([]byte, wordLength)
	binary.BigEndian.PutUint64(w[wordLength-8:], uint64(n)) // #nosec G115 -- n is a non-negative length

	return w
}

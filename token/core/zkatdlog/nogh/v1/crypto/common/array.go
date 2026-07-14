/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package common provides utility helpers used by the zkatdlog no-GH
// crypto implementation (serialization and hashing helpers for math.G1 elements).
package common

import (
	"hash"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Separator is used to delimit the end of an array of bytes.
// The bytes are the bytes of a hex-encoded string.
const Separator = "||"

// G1Array is an array of G1 elements.
type G1Array []*math.G1

// Bytes returns the serialization of the G1Array as a byte slice.
// It returns an error if any element cannot be marshaled.
func (a G1Array) Bytes() ([]byte, error) {
	raw := make([][]byte, len([]*math.G1(a)))
	for i, e := range []*math.G1(a) {
		if e == nil {
			return nil, errors.Errorf("failed to marshal array of G1")
		}
		raw[i] = e.Bytes()
	}

	return crypto.AppendFixed32([]byte{}, raw), nil
}

// BytesTo writes the serialization of the G1Array into the provided buffer b
// and returns the extended slice. The provided buffer b is cleared before use.
// It returns an error if any element cannot be marshaled.
func (a G1Array) BytesTo(b []byte) ([]byte, error) {
	raw := make([][]byte, len([]*math.G1(a)))
	for i, e := range []*math.G1(a) {
		if e == nil {
			return nil, errors.Errorf("failed to marshal array of G1")
		}
		raw[i] = e.Bytes()
	}
	// clear the provided buffer and reuse its capacity
	if len(b) != 0 {
		b = b[:0]
	}

	return crypto.AppendFixed32(b, raw), nil
}

// GetG1Array takes multiple slices of *math.G1 and concatenates them into a single *G1Array.
func GetG1Array(elements ...[]*math.G1) G1Array {
	// compute length
	length := 0
	for _, e := range elements {
		length += len(e)
	}
	s := make([]*math.G1, 0, length)
	for _, e := range elements {
		s = append(s, e...)
	}

	return s
}

// HashG1Array computes and returns the digest of the provided G1 elements
// using the supplied hash.Hash. The hash is reset before use.
func HashG1Array(h hash.Hash, elements ...*math.G1) []byte {
	h.Reset()

	for _, e := range elements {
		h.Write(e.Bytes())
	}

	return h.Sum(nil)
}

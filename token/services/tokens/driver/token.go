/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"bytes"

	"github.com/LFDT-Panurus/panurus/token/services/utils"
)

type (
	// Type represents the type identifier for a token or metadata format.
	Type = int32
)

// Token is a byte slice representing the serialized form of a token as handled by the Tokens Service.
type Token []byte

// Equal returns true if two Token representations are byte-for-byte identical.
func (id Token) Equal(id2 Token) bool {
	return bytes.Equal(id, id2)
}

// UniqueID returns a hexadecimal string representation of the hash of the token, serving as a unique identifier.
func (id Token) UniqueID() string {
	if len(id) == 0 {
		return "<empty>"
	}

	return utils.Hashable(id).String()
}

// Hash returns the raw hash string of the token.
func (id Token) Hash() string {
	if len(id) == 0 {
		return "<empty>"
	}

	return utils.Hashable(id).RawString()
}

// String returns the unique identifier for the token.
func (id Token) String() string {
	return id.UniqueID()
}

// Bytes returns the raw byte representation of the token.
func (id Token) Bytes() []byte {
	return id
}

// IsNone returns true if the token representation is empty.
func (id Token) IsNone() bool {
	return len(id) == 0
}

// Metadata is a byte slice representing serialized metadata associated with a token.
type Metadata []byte

// Equal returns true if two Metadata representations are byte-for-byte identical.
func (id Metadata) Equal(id2 Metadata) bool {
	return bytes.Equal(id, id2)
}

// UniqueID returns a hexadecimal string representation of the hash of the metadata.
func (id Metadata) UniqueID() string {
	if len(id) == 0 {
		return "<empty>"
	}

	return utils.Hashable(id).String()
}

// Hash returns the raw hash string of the metadata.
func (id Metadata) Hash() string {
	if len(id) == 0 {
		return "<empty>"
	}

	return utils.Hashable(id).RawString()
}

// String returns the unique identifier for the metadata.
func (id Metadata) String() string {
	return id.UniqueID()
}

// Bytes returns the raw byte representation of the metadata.
func (id Metadata) Bytes() []byte {
	return id
}

// IsNone returns true if the metadata representation is empty.
func (id Metadata) IsNone() bool {
	return len(id) == 0
}

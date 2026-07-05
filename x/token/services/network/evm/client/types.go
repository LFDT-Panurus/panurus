/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package client defines the EVM JSON-RPC client abstraction used by the network driver,
// together with the local value types (Address, Hash) exchanged with the chain.
//
// The types are defined locally, without go-ethereum, because that dependency's license is a
// hard blocker for the project. They are a minimal mirror of the well-known 20-byte address and
// 32-byte hash, with hex, text and JSON encoding. Addresses are rendered as lowercase hex; EIP-55
// checksum rendering can be layered on later if a UI needs it (it does not affect on-chain
// comparisons, which are by bytes).
package client

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// AddressLength is the length in bytes of an Ethereum address.
	AddressLength = 20
	// HashLength is the length in bytes of a Keccak256/SHA-256 hash.
	HashLength = 32
)

// Address is a 20-byte Ethereum account or contract address.
type Address [AddressLength]byte

// Hash is a 32-byte hash value (Keccak256 or SHA-256).
type Hash [HashLength]byte

// BytesToAddress returns an Address set to the value of b.
// If b is longer than AddressLength it is right-aligned (the low-order bytes are kept), matching
// the conventional interpretation of an address as a big-endian 160-bit value.
func BytesToAddress(b []byte) Address {
	var a Address
	if len(b) > AddressLength {
		b = b[len(b)-AddressLength:]
	}
	copy(a[AddressLength-len(b):], b)

	return a
}

// HexToAddress parses a hex string (with or without a 0x prefix) into an Address.
// It is strict: the decoded value must be exactly AddressLength bytes.
func HexToAddress(s string) (Address, error) {
	b, err := decodeHex(s)
	if err != nil {
		return Address{}, errors.Wrapf(err, "invalid address [%s]", s)
	}
	if len(b) != AddressLength {
		return Address{}, errors.Errorf("invalid address length for [%s]: expected %d bytes, got %d", s, AddressLength, len(b))
	}

	return BytesToAddress(b), nil
}

// Bytes returns the address as a byte slice.
func (a Address) Bytes() []byte { return a[:] }

// Hex returns the 0x-prefixed lowercase hex encoding of the address.
func (a Address) Hex() string { return "0x" + hex.EncodeToString(a[:]) }

// String implements fmt.Stringer, returning the 0x-prefixed hex encoding.
func (a Address) String() string { return a.Hex() }

// MarshalText implements encoding.TextMarshaler (used by JSON and YAML).
func (a Address) MarshalText() ([]byte, error) { return []byte(a.Hex()), nil }

// UnmarshalText implements encoding.TextUnmarshaler, parsing a hex string into the address.
func (a *Address) UnmarshalText(text []byte) error {
	parsed, err := HexToAddress(string(text))
	if err != nil {
		return err
	}
	*a = parsed

	return nil
}

// BytesToHash returns a Hash set to the value of b, right-aligned like BytesToAddress.
func BytesToHash(b []byte) Hash {
	var h Hash
	if len(b) > HashLength {
		b = b[len(b)-HashLength:]
	}
	copy(h[HashLength-len(b):], b)

	return h
}

// HexToHash parses a hex string (with or without a 0x prefix) into a Hash.
// It is strict: the decoded value must be exactly HashLength bytes.
func HexToHash(s string) (Hash, error) {
	b, err := decodeHex(s)
	if err != nil {
		return Hash{}, errors.Wrapf(err, "invalid hash [%s]", s)
	}
	if len(b) != HashLength {
		return Hash{}, errors.Errorf("invalid hash length for [%s]: expected %d bytes, got %d", s, HashLength, len(b))
	}

	return BytesToHash(b), nil
}

// Bytes returns the hash as a byte slice.
func (h Hash) Bytes() []byte { return h[:] }

// Hex returns the 0x-prefixed lowercase hex encoding of the hash.
func (h Hash) Hex() string { return "0x" + hex.EncodeToString(h[:]) }

// String implements fmt.Stringer, returning the 0x-prefixed hex encoding.
func (h Hash) String() string { return h.Hex() }

// MarshalText implements encoding.TextMarshaler (used by JSON and YAML).
func (h Hash) MarshalText() ([]byte, error) { return []byte(h.Hex()), nil }

// UnmarshalText implements encoding.TextUnmarshaler, parsing a hex string into the hash.
func (h *Hash) UnmarshalText(text []byte) error {
	parsed, err := HexToHash(string(text))
	if err != nil {
		return err
	}
	*h = parsed

	return nil
}

// Compile-time checks that the JSON encoders round-trip through the text encoders.
var (
	_ json.Marshaler   = Address{}
	_ json.Unmarshaler = (*Address)(nil)
	_ json.Marshaler   = Hash{}
	_ json.Unmarshaler = (*Hash)(nil)
)

// MarshalJSON encodes the address as a JSON hex string.
func (a Address) MarshalJSON() ([]byte, error) { return json.Marshal(a.Hex()) }

// UnmarshalJSON decodes a JSON hex string into the address.
func (a *Address) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.Wrapf(err, "address must be a JSON string")
	}

	return a.UnmarshalText([]byte(s))
}

// MarshalJSON encodes the hash as a JSON hex string.
func (h Hash) MarshalJSON() ([]byte, error) { return json.Marshal(h.Hex()) }

// UnmarshalJSON decodes a JSON hex string into the hash.
func (h *Hash) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.Wrapf(err, "hash must be a JSON string")
	}

	return h.UnmarshalText([]byte(s))
}

// decodeHex decodes a hex string, tolerating an optional 0x/0X prefix and surrounding whitespace.
func decodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")

	return hex.DecodeString(s)
}

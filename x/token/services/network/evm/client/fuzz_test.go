/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzHexToAddress fuzzes the address parser.
//
// Addresses arrive from configuration and from peers, and the parser is strict on purpose: an address
// that is silently truncated or zero-padded into something valid would point the driver at the wrong
// contract. So the property is not only that it does not panic, but that anything it accepts round
// trips back to the same address through Hex.
func FuzzHexToAddress(f *testing.F) {
	f.Add("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	f.Add("5FbDB2315678afecb367f032d93F642f64180aa3") // no prefix
	f.Add("0x")
	f.Add("")
	f.Add("0x5FbDB2315678afecb367f032d93F642f64180aa")   // one nibble short
	f.Add("0x5FbDB2315678afecb367f032d93F642f64180aa33") // one nibble long
	f.Add("0xzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")  // right length, not hex
	f.Add("  0x5FbDB2315678afecb367f032d93F642f64180aa3  ")

	f.Fuzz(func(t *testing.T, s string) {
		address, err := HexToAddress(s)
		if err != nil {
			return
		}

		// Re-parsing the canonical form must give the same address, or two spellings of one contract
		// could compare unequal.
		again, err := HexToAddress(address.Hex())
		if err != nil {
			t.Fatalf("the canonical form of a parsed address does not parse: %q -> %q: %v", s, address.Hex(), err)
		}
		if again != address {
			t.Fatalf("address does not round trip: %q -> %q -> %q", s, address.Hex(), again.Hex())
		}
	})
}

// FuzzHexToHash fuzzes the hash parser, which reads transaction hashes and anchors off the wire and
// out of node responses. Same round-trip property as the address parser, for the same reason: an
// anchor that parses two ways is an anchor finality cannot match on.
func FuzzHexToHash(f *testing.F) {
	f.Add("0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa")
	f.Add("853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa")
	f.Add("0x")
	f.Add("")
	f.Add(strings.Repeat("f", 63)) // one nibble short
	f.Add(strings.Repeat("f", 65)) // one nibble long
	f.Add(strings.Repeat("z", 64)) // right length, not hex

	f.Fuzz(func(t *testing.T, s string) {
		hash, err := HexToHash(s)
		if err != nil {
			return
		}
		again, err := HexToHash(hash.Hex())
		if err != nil {
			t.Fatalf("the canonical form of a parsed hash does not parse: %q -> %q: %v", s, hash.Hex(), err)
		}
		if again != hash {
			t.Fatalf("hash does not round trip: %q -> %q -> %q", s, hash.Hex(), again.Hex())
		}
	})
}

// FuzzBytesToAddress fuzzes the right-aligning byte conversion. It takes arbitrary-length input by
// design (a 32-byte ABI word is truncated to its low 20 bytes), so the property is that it never
// panics on a length it did not expect and always yields a full address.
func FuzzBytesToAddress(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, AddressLength))
	f.Add(make([]byte, AddressLength-1))
	f.Add(make([]byte, 32))
	f.Add(make([]byte, 64))

	f.Fuzz(func(t *testing.T, b []byte) {
		// The return type is a fixed-size array, so the length is a compile-time guarantee. What is
		// worth checking is that the low bytes of the input survive, which is what makes an address
		// recovered from a 32-byte ABI word the same one the contract meant.
		address := BytesToAddress(b)
		if len(b) >= AddressLength && !bytes.Equal(address[:], b[len(b)-AddressLength:]) {
			t.Fatalf("right-alignment dropped bytes: %x from %x", address, b)
		}
	})
}

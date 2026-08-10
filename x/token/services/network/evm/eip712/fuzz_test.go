/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package eip712

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// FuzzRecoverAddress fuzzes the endorsement signature parser.
//
// This is the most attacker-reachable byte slice in the driver: the signature arrives from a remote
// endorser over an FSC session, and the initiator recovers a signer from it before it has any reason
// to trust the peer. A panic here is reachable by any node that can open a session.
//
// The property is that RecoverAddress either returns an address or an error, never panics, and never
// accepts a signature the EndorsementVerifier contract would reject. The second half matters as much
// as the first: a signature accepted here but rejected on chain would let an initiator assemble a
// quorum that cannot be applied.
func FuzzRecoverAddress(f *testing.F) {
	signer, err := NewSignerFromBytes(testScalar(1))
	if err != nil {
		f.Fatalf("failed to build the seed signer: %v", err)
	}
	var digest [32]byte
	copy(digest[:], "a digest that is exactly 32 byte")
	valid, err := signer.Sign(digest)
	if err != nil {
		f.Fatalf("failed to produce a seed signature: %v", err)
	}

	f.Add(digest[:], valid)                         // a signature that must verify
	f.Add(digest[:], []byte{})                      // empty
	f.Add(digest[:], valid[:len(valid)-1])          // truncated
	f.Add(digest[:], append(valid, 0x00))           // over-long
	f.Add(digest[:], flipRecoveryID(valid))         // v outside {27,28}
	f.Add(digest[:], make([]byte, SignatureLength)) // all zeroes: a well-formed length, nothing else

	f.Fuzz(func(t *testing.T, digestRaw, sig []byte) {
		var d [32]byte
		copy(d[:], digestRaw)

		address, err := RecoverAddress(d, sig)
		if err != nil {
			return
		}

		// Anything accepted must have passed the contract's own format rules, since the two have to
		// agree for an endorsement to be applyable.
		if err := checkSignatureFormat(sig); err != nil {
			t.Fatalf("accepted a signature the contract would reject: %v", err)
		}
		if address == (client.Address{}) {
			t.Fatal("recovered the zero address without an error")
		}
	})
}

// FuzzNewSignerFromBytes fuzzes the private-key parser. The scalar comes from an operator-provisioned
// file rather than the network, so the bar is that a corrupt or truncated file is an error at startup
// rather than a panic.
func FuzzNewSignerFromBytes(f *testing.F) {
	f.Add(testScalar(1))
	f.Add([]byte{})
	f.Add(make([]byte, 32))   // the zero scalar, which is not a valid key
	f.Add(make([]byte, 31))   // one byte short
	f.Add(make([]byte, 33))   // one byte long
	f.Add([]byte{0xff, 0xff}) // far too short

	f.Fuzz(func(t *testing.T, raw []byte) {
		signer, err := NewSignerFromBytes(raw)
		if err != nil {
			return
		}
		if signer == nil {
			t.Fatal("returned a nil signer without an error")
		}
		// A usable signer must be able to sign, otherwise the failure is deferred to first use.
		var digest [32]byte
		if _, err := signer.Sign(digest); err != nil {
			t.Fatalf("accepted a key that cannot sign: %v", err)
		}
	})
}

// testScalar returns a valid 32-byte private-key scalar with the given low byte.
func testScalar(low byte) []byte {
	k := make([]byte, 32)
	k[31] = low

	return k
}

// flipRecoveryID returns a copy of sig whose recovery id is out of range.
func flipRecoveryID(sig []byte) []byte {
	out := make([]byte, len(sig))
	copy(out, sig)
	if len(out) == SignatureLength {
		out[64] = 0x07
	}

	return out
}

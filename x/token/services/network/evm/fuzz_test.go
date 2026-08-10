/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// FuzzEnvelopeFromBytes fuzzes the envelope wire decoder.
//
// An envelope crosses the wire between nodes and is handed to FromBytes before anything about it has
// been checked, so the bytes are attacker-controlled. The property is that decoding either fails or
// yields an envelope that is safe to use: in particular TxID and String must not panic on whatever
// came out, since both run on the logging and error paths where a panic is most damaging.
func FuzzEnvelopeFromBytes(f *testing.F) {
	full, err := NewApprovedEnvelope(
		"c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1",
		&statedelta.StateDelta{IsSetup: true, SetupParameters: []byte("pp")},
		[][]byte{make([]byte, 65)},
	).Bytes()
	if err != nil {
		f.Fatalf("failed to build the seed envelope: %v", err)
	}

	f.Add(full)                     // a well-formed envelope
	f.Add([]byte(`{}`))             // valid json, no fields
	f.Add([]byte(``))               // empty
	f.Add(full[:len(full)/2])       // truncated mid-document
	f.Add([]byte(`{"anchor":123}`)) // right key, wrong type
	f.Add([]byte(`{"delta":null,"endorsements":[null]}`))
	f.Add([]byte(`{"raw_tx":"!!not base64!!"}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var e Envelope
		if err := e.FromBytes(raw); err != nil {
			return
		}

		// Whatever decoded has to survive the accessors the driver calls on it unconditionally.
		_ = e.TxID()
		_ = e.String()

		// Re-marshalling must not fail: an envelope the driver accepted is one it may pass on.
		if _, err := e.Bytes(); err != nil {
			t.Fatalf("decoded an envelope that cannot be re-encoded: %v", err)
		}
	})
}

// FuzzComputeAnchor fuzzes the anchor derivation over the nonce and creator identity a request
// carries. The creator is a serialized identity that came from a peer, so neither input is trusted.
// The derivation is length-prefixed precisely so that a creator cannot forge another party's anchor
// by shifting bytes across the boundary, and this checks the boundary holds for arbitrary inputs.
func FuzzComputeAnchor(f *testing.F) {
	f.Add([]byte("nonce"), []byte("creator"))
	f.Add([]byte{}, []byte{})
	f.Add([]byte{0x00}, []byte{})
	f.Add(make([]byte, 24), []byte("alice"))
	f.Add([]byte("nonce-creator"), []byte(""))

	f.Fuzz(func(t *testing.T, nonce, creator []byte) {
		got := computeAnchor(nonce, creator)
		if len(got) != 64 {
			t.Fatalf("anchor is not a 32-byte hex string: %q", got)
		}

		// Deterministic: the same inputs must always give the same anchor, or finality by anchor
		// stops working.
		if again := computeAnchor(nonce, creator); again != got {
			t.Fatalf("anchor is not deterministic: %q then %q", got, again)
		}

		// The length prefix is what stops one party claiming another's anchor by moving the split.
		// Whenever a split is possible, moving it must change the anchor.
		if len(nonce) > 0 {
			shifted := computeAnchor(nonce[:len(nonce)-1], append([]byte{nonce[len(nonce)-1]}, creator...))
			if shifted == got {
				t.Fatalf("moving a byte across the nonce/creator boundary kept the anchor %q", got)
			}
		}
	})
}

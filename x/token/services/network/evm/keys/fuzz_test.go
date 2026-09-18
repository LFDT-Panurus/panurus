/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package keys

import (
	"encoding/hex"
	"testing"
)

// FuzzAnchorFromTxID fuzzes the anchor decoder. The string comes from a token request's TxId, which
// an endorser decodes before it has validated the request that carries it (endorsement/delta.go), and
// which a node also decodes straight off an envelope it received (network.go). Neither caller trusts
// its input yet, so the property is that decoding either fails cleanly or succeeds; it must never
// panic on malformed, truncated, or oversized hex.
func FuzzAnchorFromTxID(f *testing.F) {
	a := anchor(0x11)
	valid := hex.EncodeToString(a[:])

	f.Add(valid)                // a well-formed anchor
	f.Add("")                   // empty
	f.Add("zz")                 // non-hex
	f.Add(valid[:AnchorLength]) // truncated, still valid hex
	f.Add(valid + "ab")         // over-long, still valid hex
	f.Add("0x" + valid)         // 0x-prefixed, not accepted by hex.DecodeString

	f.Fuzz(func(t *testing.T, txID string) {
		_, _ = AnchorFromTxID(txID)
	})
}

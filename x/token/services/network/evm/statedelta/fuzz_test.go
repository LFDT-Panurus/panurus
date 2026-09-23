/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statedelta

import (
	"bytes"
	"encoding/json"
	"testing"
)

// FuzzStateDeltaValidate fuzzes the JSON decode plus Validate for StateDelta.
//
// Validate is the first thing that runs on a peer's delta - endorsement/initiator.go calls it inside
// bind, before any signature has been checked, so both the JSON that produces a StateDelta and the
// struct itself are attacker-controlled. The property: decoding and validating never panics, and a
// delta Validate accepts satisfies the bounds and strict key ordering its own doc comment promises
// (see maxDeltaEntries/maxFieldBytes and "StateDelta determinism" in
// docs/services/network-ethereum-internals.md), so nothing downstream (EIP-712 hashing, ABI encoding)
// can be handed more work than Validate's contract allows.
func FuzzStateDeltaValidate(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`{"IsSetup":true,"SetupParameters":"cHA="}`))
	f.Add([]byte(`{"IsSetup":true}`))                            // IsSetup without SetupParameters: must be rejected
	f.Add([]byte(`{"SetupParameters":"cHA="}`))                  // SetupParameters without IsSetup: must be rejected
	f.Add([]byte(`{"MetadataKeys":[],"MetadataVals":["AA=="]}`)) // asymmetric metadata
	f.Add([]byte(`{"MetadataKeys":null,"MetadataVals":null}`))
	f.Add([]byte(`{"Outputs":[{"TokenID":null,"SNMarker":null,"TokenData":null}]}`))
	f.Add(bytes.Repeat([]byte(`{`), 64)) // malformed, deeply unbalanced

	f.Fuzz(func(t *testing.T, raw []byte) {
		var d StateDelta
		if err := json.Unmarshal(raw, &d); err != nil {
			return
		}

		err := d.Validate()
		if err != nil {
			return
		}

		// Everything below is a consequence Validate's own doc comment promises whenever it accepts a
		// delta; if any of these trips, Validate accepted something it documents as rejecting.
		if len(d.SpentRefs) > maxDeltaEntries {
			t.Fatalf("Validate accepted %d spent refs, over the %d limit", len(d.SpentRefs), maxDeltaEntries)
		}
		if len(d.Outputs) > maxDeltaEntries {
			t.Fatalf("Validate accepted %d outputs, over the %d limit", len(d.Outputs), maxDeltaEntries)
		}
		if len(d.MetadataKeys) > maxDeltaEntries || len(d.MetadataVals) > maxDeltaEntries {
			t.Fatalf("Validate accepted metadata over the %d limit", maxDeltaEntries)
		}
		if len(d.MetadataKeys) != len(d.MetadataVals) {
			t.Fatalf("Validate accepted mismatched metadata lengths: %d keys, %d vals",
				len(d.MetadataKeys), len(d.MetadataVals))
		}
		for i := 1; i < len(d.MetadataKeys); i++ {
			if bytes.Compare(d.MetadataKeys[i-1][:], d.MetadataKeys[i][:]) >= 0 {
				t.Fatalf("Validate accepted metadata keys out of strict ascending order at index %d", i)
			}
		}
		if d.IsSetup != (len(d.SetupParameters) > 0) {
			t.Fatalf("Validate accepted IsSetup=%v with SetupParameters len=%d", d.IsSetup, len(d.SetupParameters))
		}
		if d.IsSetup && (len(d.SpentRefs) != 0 || len(d.Outputs) != 0 || len(d.MetadataKeys) != 0) {
			t.Fatalf("Validate accepted a setup delta carrying spent refs, outputs, or metadata")
		}
	})
}

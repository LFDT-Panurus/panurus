/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// FuzzEndorseResponseDecode fuzzes the JSON decode of an EndorseResponse plus the initiator-side
// checks Collect runs on it (initiator.go:154-166): Error(), then, on success, bind and the EIP-712
// digest, both of which touch resp.Delta directly.
//
// A response crosses the wire from an endorser the initiator does not otherwise trust to have
// behaved: it can be buggy, compromised, or byzantine, and Collect must survive whatever bytes come
// back. The property under test is Error()'s own documented guarantee - "a reply carrying a signature
// but no delta is a failure too" - which is what makes it safe for bind and Digest to dereference
// resp.Delta unconditionally once Error() is nil. Fuzzing is what would have caught it if that guard
// were ever weakened: JSON round-trips easily produce a signature with a null delta, exactly the
// shape the guard exists for.
func FuzzEndorseResponseDecode(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"err":"declined"}`))
	f.Add([]byte(`{"signature":"AQIDBA=="}`))                      // signature, no delta: must be a failure
	f.Add([]byte(`{"delta":null,"signature":"AQIDBA=="}`))         // explicit null delta with a signature
	f.Add([]byte(`{"delta":{},"signature":"AQIDBA=="}`))           // zero-value delta with a signature
	f.Add([]byte(`{"delta":{"IsSetup":true},"signature":"AQ=="}`)) // malformed delta (IsSetup without params)
	f.Add([]byte(``))
	f.Add([]byte(`not json at all`))

	var anchor [keys.AnchorLength]byte
	domain := eip712.Domain{ChainID: big.NewInt(31337), VerifyingContract: client.Address{}}

	f.Fuzz(func(t *testing.T, raw []byte) {
		var resp EndorseResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return
		}

		if err := resp.Error(); err != nil {
			// Error() itself must not have needed a non-nil Delta to reach this verdict; nothing else
			// to check on the failure path.
			return
		}

		// Error() is nil, so its own contract guarantees resp.Delta is non-nil: verify that, then
		// exercise exactly what Collect does next.
		if resp.Delta == nil {
			t.Fatalf("EndorseResponse.Error() returned nil for a response with no delta")
		}

		i := &Initiator{}
		if err := i.bind(anchor, resp.Delta); err != nil {
			return
		}
		_ = eip712.Digest(domain, resp.Delta)
	})
}

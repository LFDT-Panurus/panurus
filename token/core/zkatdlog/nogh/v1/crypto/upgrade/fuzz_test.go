/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package upgrade

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

const maxFuzzProofBytes = 256 << 10

// FuzzProofDeserializeNoPanic fuzzes Proof.Deserialize with arbitrary bytes.
// Proof is read directly from an untrusted upgrade request via
// Service.checkUpgradeProof (service.go), so any panic here is an
// unauthenticated DoS against the token-upgrade flow.
func FuzzProofDeserializeNoPanic(f *testing.F) {
	p := &Proof{
		Challenge: []byte("test-challenge"),
		Tokens: []token.LedgerToken{{
			ID:            token.ID{TxId: "tx1", Index: 1},
			Token:         []byte("token1"),
			TokenMetadata: []byte("meta1"),
			Format:        token.Format("token format1"),
		}},
		Signatures: []Signature{
			[]byte("sig1"),
		},
	}
	raw, err := p.Serialize()
	require.NoError(f, err)

	f.Add(raw)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add([]byte("{"))
	f.Add(raw[:len(raw)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = (&Proof{}).Deserialize(raw)
		})
	})
}

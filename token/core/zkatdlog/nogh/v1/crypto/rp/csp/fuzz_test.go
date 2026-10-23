/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package csp

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/stretchr/testify/require"
)

const maxFuzzCSPRangeProofBytes = 256 << 10

// FuzzCSPRangeProofDeserializeNoPanic fuzzes RangeProof.Deserialize/Validate/Verify with
// arbitrary bytes against a fixed, valid public statement. This is the shape of call a
// transfer/issue verifier makes on unauthenticated wire bytes (see
// transfer.CSPVerifier.Verify), so any panic anywhere in the Deserialize/Validate/Verify
// chain — not just Deserialize itself — is an unauthenticated validator DoS. Validate
// alone is not sufficient coverage here: e.g. a foreign-curve scalar surviving Validate's
// own curve checks can still panic deep inside Verify via a cross-curve backend type
// assertion (see the comment on proof.u/proof.sEval in validateRangeProof).
func FuzzCSPRangeProofDeserializeNoPanic(f *testing.F) {
	setup, err := newRPSetup(math.Curves[math.BN254], 8, 42)
	require.NoError(f, err)

	proof, err := setup.prover.Prove()
	require.NoError(f, err)
	validBytes, err := proof.Serialize()
	require.NoError(f, err)

	f.Add(validBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(validBytes[:len(validBytes)/2])

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzCSPRangeProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			p := &RangeProof{}
			if err := p.Deserialize(raw); err != nil {
				return
			}
			if err := p.Validate(math.BN254); err != nil {
				return
			}
			_ = setup.verifier.Verify(p)
		})
	})
}

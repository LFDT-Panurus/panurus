/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	"github.com/stretchr/testify/require"
)

const maxFuzzTransferProofBytes = 256 << 10

// FuzzBulletProofVerifierNoPanic fuzzes transfer.BulletProofVerifier.Verify with
// arbitrary proof bytes. This is the exact call TransferValidate makes on
// unauthenticated wire bytes, so any panic anywhere in the
// Deserialize/Validate/Verify chain is an unauthenticated validator DoS. Uses a
// 2-input/2-output transfer so RangeCorrectness is populated (a 1-in/1-out
// ownership transfer skips it entirely).
func FuzzBulletProofVerifierNoPanic(f *testing.F) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.RangeProofType)
	require.NoError(f, err)

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(f, err)
	prover, err := transfer.NewBulletProofProver(intw, outtw, in, out, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(proofBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(proofBytes[:len(proofBytes)/2])

	verifier := transfer.NewBulletProofVerifier(in, out, pp)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTransferProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = verifier.Verify(raw)
		})
	})
}

// FuzzCSPVerifierNoPanic mirrors FuzzBulletProofVerifierNoPanic for the CSP-based
// transfer proof verifier.
func FuzzCSPVerifierNoPanic(f *testing.F) {
	pp, err := setupWithProofType(TestBits, TestCurve, rp.CSPRangeProofType)
	require.NoError(f, err)

	intw, outtw, in, out, err := prepareInputsForZKTransfer(pp, 2, 2)
	require.NoError(f, err)
	prover, err := transfer.NewCSPBasedProver(intw, outtw, in, out, pp)
	require.NoError(f, err)
	proofBytes, err := prover.Prove()
	require.NoError(f, err)

	f.Add(proofBytes)
	f.Add([]byte{})
	f.Add([]byte("malformed"))
	f.Add(proofBytes[:len(proofBytes)/2])

	verifier := transfer.NewCSPVerifier(in, out, pp)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxFuzzTransferProofBytes {
			t.Skip()
		}
		require.NotPanics(t, func() {
			_ = verifier.Verify(raw)
		})
	})
}

/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
)

// TestVerifyBindingSignature_AgreesWithProverSideConstruction is the most
// important test in this file: it independently proves that a signature
// produced by prover.ComputeBindingSignature, given ONLY the decoded
// public bytes a real wire-format action would carry (not the original
// prover.ProofResult values with their private RCV fields), still verifies
// through this package's reconstruction path.
func TestVerifyBindingSignature_AgreesWithProverSideConstruction(t *testing.T) {
	rcv1, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)
	rcv2, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)

	cv1, err := jubjub.ValueCommit(100, rcv1)
	require.NoError(t, err)
	cv2, err := jubjub.ValueCommit(100, rcv2)
	require.NoError(t, err)

	var cm1, cm2 fr.Element
	_, _ = cm1.SetRandom()
	_, _ = cm2.SetRandom()

	inputs := []prover.ProofResult{{Commitment: cm1, ValueCommit: cv1, RCV: rcv1}}
	outputs := []prover.ProofResult{{Commitment: cm2, ValueCommit: cv2, RCV: rcv2}}

	sig, err := prover.ComputeBindingSignature("transfer", "USD", inputs, outputs)
	require.NoError(t, err)
	sigBytes, err := jubjub.SerializeSignature(sig)
	require.NoError(t, err)

	// Simulate what the validator actually has: decoded public data only,
	// with RCV never populated (as decode.go never produces it).
	decodedIn := []decodedSpend{{Commitment: cm1, ValueCommitX: cv1.X, ValueCommitY: cv1.Y}}
	decodedOut := []decodedOutput{{Commitment: cm2, ValueCommitX: cv2.X, ValueCommitY: cv2.Y}}

	err = verifyBindingSignature("transfer", "USD", decodedIn, decodedOut, sigBytes, 0)
	require.NoError(t, err)
}

func TestVerifyBindingSignature_RejectsCorruptedSignatureBytes(t *testing.T) {
	corrupted := make([]byte, 96) // all-zero, not a valid signature
	err := verifyBindingSignature("transfer", "USD", nil, nil, corrupted, 0)
	require.Error(t, err)
}

func TestVerifyBindingSignature_WrongPublicValueDeltaFailsIssuance(t *testing.T) {
	rcv, err := jubjub.RandomJubjubScalar()
	require.NoError(t, err)

	cv, err := jubjub.ValueCommit(500, rcv)
	require.NoError(t, err)
	var cm fr.Element
	_, _ = cm.SetRandom()

	outputs := []prover.ProofResult{{Commitment: cm, ValueCommit: cv, RCV: rcv}}
	var totalIssued fr.Element
	totalIssued.SetUint64(500)

	sig, err := prover.ComputeBindingSignature("issue", "USD", nil, outputs)
	require.NoError(t, err)
	sigBytes, err := jubjub.SerializeSignature(sig)
	require.NoError(t, err)

	decodedOut := []decodedOutput{{Commitment: cm, ValueCommitX: cv.X, ValueCommitY: cv.Y}}

	// Deliberately verify with 0 instead of the real
	// totalIssued, this is exactly the "forgot to pass TotalValue" bug
	// this test exists to catch permanently.
	err = verifyBindingSignature("issue", "USD", nil, decodedOut, sigBytes, 0)
	require.Error(t, err, "wrong publicValueDelta must fail, this is the exact bug class already found once in prover/binding_test.go")
}

/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validator

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

func mustTypeCommitmentV(t *testing.T, tokenType string) [32]byte {
	t.Helper()
	var typeRandomness fr.Element
	_, err := typeRandomness.SetRandom()
	require.NoError(t, err)

	tr := typeRandomness.Bytes()
	tcBytes, err := snarktoken.ComputeTypeCommitment(tokenType, tr[:], ecc.BLS12_381)
	require.NoError(t, err)

	var tc [32]byte
	require.Equal(t, 32, copy(tc[:], tcBytes))

	return tc
}

// TestVerifyBindingSignature_AgreesWithProverSideConstruction is the most
// important test in this file: it independently proves that a signature
// produced by prover.ComputeBindingSignature, given ONLY the decoded
// public bytes a real wire-format action would carry (not the original
// prover.ProofResult values with their private RCV fields), still verifies
// through this package's reconstruction path.
func TestVerifyBindingSignature_AgreesWithProverSideConstruction(t *testing.T) {
	rcv1, err := edwards.Jubjub().RandomScalar()
	require.NoError(t, err)
	rcv2, err := edwards.Jubjub().RandomScalar()
	require.NoError(t, err)

	cv1, err := edwards.Jubjub().ValueCommit(100, rcv1)
	require.NoError(t, err)
	cv2, err := edwards.Jubjub().ValueCommit(100, rcv2)
	require.NoError(t, err)

	var cm1, cm2 fr.Element
	_, _ = cm1.SetRandom()
	_, _ = cm2.SetRandom()

	tc := mustTypeCommitmentV(t, "USD")
	tcBytes := tc

	cm1Bytes := cm1.Bytes()
	cv1XBytes := cv1.X.Bytes()
	cv1YBytes := cv1.Y.Bytes()
	rcv1Bytes := rcv1.Bytes()

	cm2Bytes := cm2.Bytes()
	cv2XBytes := cv2.X.Bytes()
	cv2YBytes := cv2.Y.Bytes()
	rcv2Bytes := rcv2.Bytes()

	inputs := []prover.ProofResult{{
		Commitment:       cm1Bytes[:],
		ValueCommitmentX: cv1XBytes[:],
		ValueCommitmentY: cv1YBytes[:],
		RCV:              rcv1Bytes[:],
	}}
	outputs := []prover.ProofResult{{
		Commitment:       cm2Bytes[:],
		ValueCommitmentX: cv2XBytes[:],
		ValueCommitmentY: cv2YBytes[:],
		RCV:              rcv2Bytes[:],
	}}

	sig, err := prover.ComputeBindingSignature(ecc.BLS12_381, "transfer", tcBytes[:], inputs, outputs)
	require.NoError(t, err)
	sigBytes := sig

	// Simulate what the validator actually has: decoded public data only,
	// with RCV never populated (as decode.go never produces it).
	decodedIn := []decodedSpend{{CommitmentBytes: cm1Bytes[:], ValueCommitXBytes: cv1XBytes[:], ValueCommitYBytes: cv1YBytes[:]}}
	decodedOut := []decodedOutput{{CommitmentBytes: cm2Bytes[:], ValueCommitXBytes: cv2XBytes[:], ValueCommitYBytes: cv2YBytes[:]}}

	err = verifyBindingSignature(ecc.BLS12_381, "transfer", tcBytes[:], decodedIn, decodedOut, sigBytes, 0)
	require.NoError(t, err)
}

func TestVerifyBindingSignature_RejectsCorruptedSignatureBytes(t *testing.T) {
	corrupted := make([]byte, 96) // all-zero, not a valid signature
	tc := mustTypeCommitmentV(t, "USD")

	err := verifyBindingSignature(ecc.BLS12_381, "transfer", tc[:], nil, nil, corrupted, 0)
	require.Error(t, err)
}

func TestVerifyBindingSignature_WrongPublicValueDeltaFailsIssuance(t *testing.T) {
	rcv, err := edwards.Jubjub().RandomScalar()
	require.NoError(t, err)

	cv, err := edwards.Jubjub().ValueCommit(500, rcv)
	require.NoError(t, err)
	var cm fr.Element
	_, _ = cm.SetRandom()

	tc := mustTypeCommitmentV(t, "USD")
	tcBytes := tc

	cmBytes := cm.Bytes()
	cvXBytes := cv.X.Bytes()
	cvYBytes := cv.Y.Bytes()
	rcvBytes := rcv.Bytes()

	outputs := []prover.ProofResult{{
		Commitment:       cmBytes[:],
		ValueCommitmentX: cvXBytes[:],
		ValueCommitmentY: cvYBytes[:],
		RCV:              rcvBytes[:],
	}}

	sig, err := prover.ComputeBindingSignature(ecc.BLS12_381, "issue", tcBytes[:], nil, outputs)
	require.NoError(t, err)
	sigBytes := sig

	decodedOut := []decodedOutput{{CommitmentBytes: cmBytes[:], ValueCommitXBytes: cvXBytes[:], ValueCommitYBytes: cvYBytes[:]}}

	// Deliberately verify with 0 instead of the real
	// totalIssued, this is exactly the "forgot to pass TotalValue" bug
	// this test exists to catch permanently.
	err = verifyBindingSignature(ecc.BLS12_381, "issue", tcBytes[:], nil, decodedOut, sigBytes, 0)
	require.Error(t, err, "wrong publicValueDelta must fail, this is the exact bug class already found once in prover/binding_test.go")
}

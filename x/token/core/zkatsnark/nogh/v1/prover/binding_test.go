/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

func verifyJubjub(bvk edwards.Coords, msg, sig []byte) error {
	suite, err := edwards.ForCurve(ecc.BLS12_381)
	if err != nil {
		return err
	}

	return suite.VerifyBinding(bvk, msg, sig)
}

func mustProofResult(t *testing.T, value uint64) prover.ProofResult {
	t.Helper()
	rcv, err := edwards.Jubjub().RandomScalar()
	require.NoError(t, err)

	cv, err := edwards.Jubjub().ValueCommit(value, rcv)
	require.NoError(t, err)

	// Commitment field is irrelevant to binding-signature math directly,
	// but is used for sorting
	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)

	cmBytes := cm.Bytes()
	cvx := cv.X.Bytes()
	cvy := cv.Y.Bytes()
	rcvBytes := rcv.Bytes()

	return prover.ProofResult{
		Commitment:       cmBytes[:],
		ValueCommitmentX: cvx[:],
		ValueCommitmentY: cvy[:],
		RCV:              rcvBytes[:],
	}
}

func mustTypeCommitment(t *testing.T, tokenType string) []byte {
	t.Helper()
	var typeRandomness fr.Element
	_, err := typeRandomness.SetRandom()
	require.NoError(t, err)

	typeRandomnessBytes := typeRandomness.Bytes()
	tc, err := snarktoken.ComputeTypeCommitment(tokenType, typeRandomnessBytes[:], ecc.BLS12_381)
	require.NoError(t, err)

	return tc
}

func TestActionHashDeterminism(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100)}
	out := []prover.ProofResult{mustProofResult(t, 100)}
	tc := mustTypeCommitment(t, "USD")

	h1 := prover.ComputeActionHash("transfer", tc, in, out)
	h2 := prover.ComputeActionHash("transfer", tc, in, out)

	require.Equal(t, h1, h2)
}

func TestActionHashOrderIndependence(t *testing.T) {
	pr1 := mustProofResult(t, 50)
	pr2 := mustProofResult(t, 100)
	tc := mustTypeCommitment(t, "USD")

	h1 := prover.ComputeActionHash("transfer", tc, []prover.ProofResult{pr1, pr2}, nil)
	h2 := prover.ComputeActionHash("transfer", tc, []prover.ProofResult{pr2, pr1}, nil)

	require.Equal(t, h1, h2, "hash must be independent of input slice order — this is what makes "+
		"the hash canonical regardless of goroutine completion order")
}

func TestActionHashSensitivity(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100)}
	out := []prover.ProofResult{mustProofResult(t, 100)}
	tc := mustTypeCommitment(t, "USD")

	h := prover.ComputeActionHash("transfer", tc, in, out)

	withDifferentActionType := prover.ComputeActionHash("issue", tc, in, out)
	require.NotEqual(t, h, withDifferentActionType)

	tcEUR := mustTypeCommitment(t, "EUR")
	withDifferentTypeCommitment := prover.ComputeActionHash("transfer", tcEUR, in, out)
	require.NotEqual(t, h, withDifferentTypeCommitment)

	differentOut := []prover.ProofResult{mustProofResult(t, 999)}
	withDifferentOutputs := prover.ComputeActionHash("transfer", tc, in, differentOut)
	require.NotEqual(t, h, withDifferentOutputs)
}

// TestComputeBindingSignatureValid is the standard success case: conserved
// values, sign, independently recompute bvk from public data alone, verify.
func TestComputeBindingSignatureValid(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100), mustProofResult(t, 50)}
	out := []prover.ProofResult{mustProofResult(t, 120), mustProofResult(t, 30)}
	tc := mustTypeCommitment(t, "USD")

	h := prover.ComputeActionHash("transfer", tc, in, out)

	sig, err := prover.ComputeBindingSignature(ecc.BLS12_381, "transfer", tc, in, out)
	require.NoError(t, err)

	bvk, err := prover.ComputeBVK(ecc.BLS12_381, in, out, 0)
	require.NoError(t, err)
	err = verifyJubjub(bvk, h, sig)

	require.NoError(t, err, "binding signature must verify against independently computed bvk")
}

// TestComputeBindingSignatureRejectsNonConservation is the core security
// property test.
func TestComputeBindingSignatureRejectsNonConservation(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100), mustProofResult(t, 50)}
	out := []prover.ProofResult{mustProofResult(t, 120), mustProofResult(t, 40)}
	tc := mustTypeCommitment(t, "USD")

	h := prover.ComputeActionHash("transfer", tc, in, out)

	sig, err := prover.ComputeBindingSignature(ecc.BLS12_381, "transfer", tc, in, out)
	require.NoError(t, err)

	bvk, err := prover.ComputeBVK(ecc.BLS12_381, in, out, 0)
	require.NoError(t, err)
	err = verifyJubjub(bvk, h, sig)

	require.Error(t, err,
		"CRITICAL: a binding signature for non-conserved values must fail verification — "+
			"if this passes, the conservation proof is fundamentally broken")
}

func TestComputeBindingSignatureIssuance(t *testing.T) {
	out := []prover.ProofResult{mustProofResult(t, 500)}
	tc := mustTypeCommitment(t, "USD")

	sig, err := prover.ComputeBindingSignature(ecc.BLS12_381, "issue", tc, nil, out)
	require.NoError(t, err)

	bvk, err := prover.ComputeBVK(ecc.BLS12_381, nil, out, 500)
	require.NoError(t, err)
	actionHash := prover.ComputeActionHash("issue", tc, nil, out)

	err = verifyJubjub(bvk, actionHash, sig)
	require.NoError(t, err, "issuance binding signature (zero inputs) must verify")
}

func TestComputeBindingSignatureRejectsWrongTypeCommitmentAtVerification(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100)}
	out := []prover.ProofResult{mustProofResult(t, 100)}
	tcUSD := mustTypeCommitment(t, "USD")

	sig, err := prover.ComputeBindingSignature(ecc.BLS12_381, "transfer", tcUSD, in, out)
	require.NoError(t, err)

	bvk, err := prover.ComputeBVK(ecc.BLS12_381, in, out, 0)
	require.NoError(t, err)
	tcEUR := mustTypeCommitment(t, "EUR")
	h := prover.ComputeActionHash("transfer", tcEUR, in, out)

	err = verifyJubjub(bvk, h, sig)
	require.Error(t, err,
		"verifying against a hash computed with a different type commitment must fail, "+
			"this confirms type commitment really is bound into the signed message")
}

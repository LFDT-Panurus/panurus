/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
)

func mustProofResult(t *testing.T, value uint64) prover.ProofResult {
	t.Helper()
	var rcv fr.Element
	_, err := rcv.SetRandom()
	require.NoError(t, err)

	cv, err := jubjub.ValueCommit(value, rcv)
	require.NoError(t, err)

	// Commitment field is irrelevant to binding-signature math directly,
	// but is used for sorting
	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)

	return prover.ProofResult{Commitment: cm, ValueCommit: cv, RCV: rcv}
}

func TestActionHashDeterminism(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100)}
	out := []prover.ProofResult{mustProofResult(t, 100)}

	h1 := prover.ComputeActionHash("transfer", "USD", in, out)
	h2 := prover.ComputeActionHash("transfer", "USD", in, out)

	require.Equal(t, h1, h2)
}

func TestActionHashOrderIndependence(t *testing.T) {
	pr1 := mustProofResult(t, 50)
	pr2 := mustProofResult(t, 100)

	h1 := prover.ComputeActionHash("transfer", "USD", []prover.ProofResult{pr1, pr2}, nil)
	h2 := prover.ComputeActionHash("transfer", "USD", []prover.ProofResult{pr2, pr1}, nil)

	require.Equal(t, h1, h2, "hash must be independent of input slice order — this is what makes "+
		"the hash canonical regardless of goroutine completion order")
}

func TestActionHashSensitivity(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100)}
	out := []prover.ProofResult{mustProofResult(t, 100)}

	h := prover.ComputeActionHash("transfer", "USD", in, out)

	withDifferentActionType := prover.ComputeActionHash("issue", "USD", in, out)
	require.NotEqual(t, h, withDifferentActionType)

	withDifferentTokenType := prover.ComputeActionHash("transfer", "EUR", in, out)
	require.NotEqual(t, h, withDifferentTokenType)

	differentOut := []prover.ProofResult{mustProofResult(t, 999)}
	withDifferentOutputs := prover.ComputeActionHash("transfer", "USD", in, differentOut)
	require.NotEqual(t, h, withDifferentOutputs)
}

// TestComputeBindingSignatureValid is the standard success case: conserved
// values, sign, independently recompute bvk from public data alone, verify.
func TestComputeBindingSignatureValid(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100), mustProofResult(t, 50)}
	out := []prover.ProofResult{mustProofResult(t, 120), mustProofResult(t, 30)}

	h := prover.ComputeActionHash("transfer", "USD", in, out)

	sig, err := prover.ComputeBindingSignature("transfer", "USD", in, out)
	require.NoError(t, err)

	bvk := prover.ComputeBVK(in, out, 0)
	err = jubjub.Verify(bvk, h, sig, jubjub.R)

	require.NoError(t, err, "binding signature must verify against independently computed bvk")
}

// TestComputeBindingSignatureRejectsNonConservation is the core security
// property test. It does NOT special-case the mismatch, it demonstrates
// that the same verification code path used for the honest case correctly
// rejects a bundle where values do not balance, purely as a mathematical
// consequence of V and R being independent generators (nobody knows
// log_R(V)).
func TestComputeBindingSignatureRejectsNonConservation(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100), mustProofResult(t, 50)}
	out := []prover.ProofResult{mustProofResult(t, 120), mustProofResult(t, 40)}

	h := prover.ComputeActionHash("transfer", "USD", in, out)

	sig, err := prover.ComputeBindingSignature("transfer", "USD", in, out)
	require.NoError(t, err)

	bvk := prover.ComputeBVK(in, out, 0)
	err = jubjub.Verify(bvk, h, sig, jubjub.R)

	require.Error(t, err,
		"CRITICAL: a binding signature for non-conserved values must fail verification — "+
			"if this passes, the conservation proof is fundamentally broken")
}

func TestComputeBindingSignatureIssuance(t *testing.T) {
	out := []prover.ProofResult{mustProofResult(t, 500)}

	sig, err := prover.ComputeBindingSignature("issue", "USD", nil, out)
	require.NoError(t, err)

	bvk := prover.ComputeBVK(nil, out, 500)
	actionHash := prover.ComputeActionHash("issue", "USD", nil, out)

	err = jubjub.Verify(bvk, actionHash, sig, jubjub.R)
	require.NoError(t, err, "issuance binding signature (zero inputs) must verify")
}

func TestComputeBindingSignatureRejectsWrongTokenTypeAtVerification(t *testing.T) {
	in := []prover.ProofResult{mustProofResult(t, 100)}
	out := []prover.ProofResult{mustProofResult(t, 100)}

	sig, err := prover.ComputeBindingSignature("transfer", "USD", in, out)
	require.NoError(t, err)

	bvk := prover.ComputeBVK(in, out, 0)
	h := prover.ComputeActionHash("transfer", "EUR", in, out)

	err = jubjub.Verify(bvk, h, sig, jubjub.R)
	require.Error(t, err,
		"verifying against a hash computed with the wrong token type must fail, "+
			"this confirms token type really is bound into the signed message")
}

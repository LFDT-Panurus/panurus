/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"

	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
)

func babyProofResult(t *testing.T, value uint64) prover.ProofResult {
	t.Helper()
	c := edwards.BabyJubjub()
	rcv, err := c.RandomScalar()
	require.NoError(t, err)
	cv, err := c.ValueCommit(value, rcv)
	require.NoError(t, err)

	var cm fr.Element
	_, err = cm.SetRandom()
	require.NoError(t, err)

	cmB, cvx, cvy, rcvB := cm.Bytes(), cv.X.Bytes(), cv.Y.Bytes(), rcv.Bytes()

	return prover.ProofResult{Commitment: cmB[:], ValueCommitmentX: cvx[:], ValueCommitmentY: cvy[:], RCV: rcvB[:]}
}

// TestBindingSignatureBabyJubjub exercises the curve-generic binding path on BN254.
func TestBindingSignatureBabyJubjub(t *testing.T) {
	suite, err := edwards.ForCurve(ecc.BN254)
	require.NoError(t, err)
	tc := make([]byte, 32)

	in := []prover.ProofResult{babyProofResult(t, 60), babyProofResult(t, 40)}
	out := []prover.ProofResult{babyProofResult(t, 100)}
	sig, err := prover.ComputeBindingSignature(ecc.BN254, "transfer", tc, in, out)
	require.NoError(t, err)
	bvk, err := prover.ComputeBVK(ecc.BN254, in, out, 0)
	require.NoError(t, err)
	require.NoError(t, suite.VerifyBinding(bvk, prover.ComputeActionHash("transfer", tc, in, out), sig))

	// Issuance: no inputs, valueBalance = Σv_out.
	iss := []prover.ProofResult{babyProofResult(t, 500)}
	isig, err := prover.ComputeBindingSignature(ecc.BN254, "issue", tc, nil, iss)
	require.NoError(t, err)
	ibvk, err := prover.ComputeBVK(ecc.BN254, nil, iss, 500)
	require.NoError(t, err)
	require.NoError(t, suite.VerifyBinding(ibvk, prover.ComputeActionHash("issue", tc, nil, iss), isig))

	// Wrong balance must not verify.
	badBvk, err := prover.ComputeBVK(ecc.BN254, nil, iss, 499)
	require.NoError(t, err)
	require.Error(t, suite.VerifyBinding(badBvk, prover.ComputeActionHash("issue", tc, nil, iss), isig))

	// Malformed RCV is rejected.
	bad := []prover.ProofResult{{RCV: []byte{1}}}
	_, err = prover.ComputeBindingSignature(ecc.BN254, "issue", tc, nil, bad)
	require.Error(t, err)

	// Unsupported curve is rejected.
	_, err = prover.ComputeBindingSignature(ecc.BW6_761, "issue", tc, nil, iss)
	require.Error(t, err)
}

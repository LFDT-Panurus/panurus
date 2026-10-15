/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/frontend"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

var (
	spendProver     *prover.SpendProver
	outputProver    *prover.OutputProver
	proverSetupOnce sync.Once
)

func setupProvers(t *testing.T) {
	t.Helper()
	proverSetupOnce.Do(func() {
		compileTestCircuits(t)

		var err error
		testPP, err = setup.SetupAll(testPP)
		if err != nil {
			panic(fmt.Sprintf("setup.SetupAll failed: %v", err))
		}

		b, err := backend.NewProofSystemBackend(testPP.ProofSystem, testPP.Curve)
		if err != nil {
			panic(fmt.Sprintf("backend creation failed: %v", err))
		}

		spendProver = prover.NewSpendProver(testSpendCS, testPP.PKSpend, b, testPP)
		outputProver = prover.NewOutputProver(testOutputCS, testPP.PKOutput, b, testPP)
	})
}

func TestSpendProver(t *testing.T) {
	setupProvers(t)

	note, err := snarktoken.NewRandomNote(500, "USD")
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	witnessRes, err := prover.BuildSpendWitness(note, tr[:], testPP)
	require.NoError(t, err)

	proof, err := spendProver.Prove(witnessRes.Assignment)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	b, err := backend.NewProofSystemBackend(testPP.ProofSystem, testPP.Curve)
	require.NoError(t, err)

	err = b.Verify(testPP.VKSpend, proof, publicWitness)
	require.NoError(t, err, "a proof from the production SpendProver must verify")
}

func TestOutputProver(t *testing.T) {
	setupProvers(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	witnessRes, err := prover.BuildOutputWitness(750, "EUR", testPP, tr[:])
	require.NoError(t, err)

	proof, err := outputProver.Prove(witnessRes.Assignment)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	b, err := backend.NewProofSystemBackend(testPP.ProofSystem, testPP.Curve)
	require.NoError(t, err)

	err = b.Verify(testPP.VKOutput, proof, publicWitness)
	require.NoError(t, err, "a proof from the production OutputProver must verify")
}

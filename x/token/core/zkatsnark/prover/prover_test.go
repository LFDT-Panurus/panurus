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
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/frontend"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

var (
	spendProver     *prover.SpendProver
	spendVK         groth16.VerifyingKey
	outputProver    *prover.OutputProver
	outputVK        groth16.VerifyingKey
	proverSetupOnce sync.Once
)

func setupProvers(t *testing.T) {
	t.Helper()
	proverSetupOnce.Do(func() {
		compileTestCircuits(t)

		spendPK, vk1, err := groth16.Setup(testSpendCS)
		if err != nil {
			panic(fmt.Sprintf("spend groth16.Setup failed: %v", err))
		}
		spendVK = vk1
		spendProver = prover.NewSpendProver(testSpendCS, spendPK)

		outputPK, vk2, err := groth16.Setup(testOutputCS)
		if err != nil {
			panic(fmt.Sprintf("output groth16.Setup failed: %v", err))
		}
		outputVK = vk2
		outputProver = prover.NewOutputProver(testOutputCS, outputPK)
	})
}

func TestSpendProver(t *testing.T) {
	setupProvers(t)

	note, err := snarktoken.NewRandomNote(500, "USD")
	require.NoError(t, err)

	witnessRes, err := prover.BuildSpendWitness(note)
	require.NoError(t, err)

	proof, err := spendProver.Prove(witnessRes.Assignment)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	err = groth16.Verify(proof, spendVK, publicWitness)
	require.NoError(t, err, "a proof from the production SpendProver must verify")
}

func TestOutputProver(t *testing.T) {
	setupProvers(t)

	witnessRes, err := prover.BuildOutputWitness(750, "EUR", testPP)
	require.NoError(t, err)

	proof, err := outputProver.Prove(witnessRes.Assignment)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	err = groth16.Verify(proof, outputVK, publicWitness)
	require.NoError(t, err, "a proof from the production OutputProver must verify")
}

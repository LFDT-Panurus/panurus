/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/frontend"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
)

var (
	spendProver      *prover.SpendProver
	outputProver     *prover.OutputProver
	testProverParams *prepared.Prover
	proverSetupOnce  sync.Once
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

		testProverParams, err = prepared.NewProver(testPP)
		if err != nil {
			panic(fmt.Sprintf("prepared.NewProver failed: %v", err))
		}

		spendProver = prover.NewSpendProver(testProverParams)
		outputProver = prover.NewOutputProver(testProverParams)
	})
}

func TestSpendProver(t *testing.T) {
	setupProvers(t)

	note, err := snarktoken.NewRandomNote(500, "USD", testPP.Curve)
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	witnessRes, err := prover.BuildSpendWitness(note, tr[:], testPP)
	require.NoError(t, err)

	proof, err := spendProver.Prove(witnessRes.Assignment)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, testPP.Curve.ScalarField())
	require.NoError(t, err)

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	vk, err := testProverParams.Backend().NewVerifyingKey(testPP.VKSpend)
	require.NoError(t, err)

	err = testProverParams.Backend().Verify(vk, proof, publicWitness)
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

	witness, err := frontend.NewWitness(witnessRes.Assignment, testPP.Curve.ScalarField())
	require.NoError(t, err)

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	vk, err := testProverParams.Backend().NewVerifyingKey(testPP.VKOutput)
	require.NoError(t, err)

	err = testProverParams.Backend().Verify(vk, proof, publicWitness)
	require.NoError(t, err, "a proof from the production OutputProver must verify")
}

/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package prover_test

import (
	"sync"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/setup"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/token"
)

var (
	testSpendCS  constraint.ConstraintSystem
	testOutputCS constraint.ConstraintSystem
	testPP       *pp.PublicParams
	compileOnce  sync.Once
)

func compileTestCircuits(t *testing.T) {
	t.Helper()
	compileOnce.Do(func() {
		testPP = pp.DefaultPublicParams()
		var err error
		testSpendCS, err = setup.CompileSpendCircuit(testPP)
		if err != nil {
			panic(err)
		}
		testOutputCS, err = setup.CompileOutputCircuit(testPP)
		if err != nil {
			panic(err)
		}
	})
}

func TestBuildSpendWitness_Satisfiable(t *testing.T) {
	compileTestCircuits(t)

	note, err := snarktoken.NewRandomNote(500, "USD")
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	witnessRes, err := prover.BuildSpendWitness(note, typeRandomness)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = testSpendCS.IsSolved(witness)
	require.NoError(t, err, "witness built from a valid Note must satisfy SpendCircuit")
}

func TestBuildSpendWitness_CommitmentMatchesNote(t *testing.T) {
	note, err := snarktoken.NewRandomNote(500, "USD")
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	witnessRes, err := prover.BuildSpendWitness(note, typeRandomness)
	require.NoError(t, err)

	commitment, err := note.Commitment()
	require.NoError(t, err)

	require.Equal(t, commitment.Bytes(), witnessRes.Commitment.Bytes(),
		"BuildSpendWitness commitment must match note.Commitment() directly")
}

func TestBuildSpendWitness_NilNote(t *testing.T) {
	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	_, err := prover.BuildSpendWitness(nil, typeRandomness)
	require.Error(t, err)
}

func TestBuildOutputWitness_Satisfiable(t *testing.T) {
	compileTestCircuits(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	witnessRes, err := prover.BuildOutputWitness(500, "USD", pp.DefaultPublicParams(), typeRandomness)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = testOutputCS.IsSolved(witness)
	require.NoError(t, err, "witness built for a fresh output must satisfy OutputCircuit")
}

func TestBuildOutputWitness_ReturnsSpendableNote(t *testing.T) {
	compileTestCircuits(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	wr, err := prover.BuildOutputWitness(500, "USD", pp.DefaultPublicParams(), typeRandomness)
	require.NoError(t, err)
	require.NotNil(t, wr.Note)

	// The returned Note must, when opened, reproduce the same commitment
	// published in the OutputCircuit assignment, otherwise this newly
	// created token would be unspendable by whoever receives the Note.
	commitment, err := wr.Note.Commitment()
	require.NoError(t, err)
	require.Equal(t, wr.Commitment.Bytes(), commitment.Bytes())
}

func TestBuildOutputWitness_ZeroValueRejectedBySolver(t *testing.T) {
	compileTestCircuits(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()

	wr, err := prover.BuildOutputWitness(0, "USD", testPP, typeRandomness)
	require.NoError(t, err, "BuildOutputWitness itself does not enforce value > 0")

	witness, err := frontend.NewWitness(wr.Assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = testOutputCS.IsSolved(witness)
	require.Error(t, err,
		"a zero-value output witness must fail OutputCircuit's range constraint, "+
			"confirming the circuit itself is the enforcement point, not the witness builder")
}

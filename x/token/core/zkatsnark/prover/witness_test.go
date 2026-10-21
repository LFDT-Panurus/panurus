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
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
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

	note, err := snarktoken.NewRandomNote(500, "USD", testPP.Curve)
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	witnessRes, err := prover.BuildSpendWitness(note, tr[:], testPP)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, testPP.Curve.ScalarField())
	require.NoError(t, err)

	err = testSpendCS.IsSolved(witness)
	require.NoError(t, err, "witness built from a valid Note must satisfy SpendCircuit")
}

func TestBuildSpendWitness_CommitmentMatchesNote(t *testing.T) {
	note, err := snarktoken.NewRandomNote(500, "USD", testPP.Curve)
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	witnessRes, err := prover.BuildSpendWitness(note, tr[:], testPP)
	require.NoError(t, err)

	commitment, err := note.Commitment(testPP.Curve)
	require.NoError(t, err)

	require.Equal(t, commitment, witnessRes.Commitment,
		"BuildSpendWitness commitment must match note.Commitment() directly")
}

func TestBuildSpendWitness_NilNote(t *testing.T) {
	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	_, err := prover.BuildSpendWitness(nil, tr[:], testPP)
	require.Error(t, err)
}

func TestBuildOutputWitness_Satisfiable(t *testing.T) {
	compileTestCircuits(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	witnessRes, err := prover.BuildOutputWitness(500, "USD", testPP, tr[:])
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, testPP.Curve.ScalarField())
	require.NoError(t, err)

	err = testOutputCS.IsSolved(witness)
	require.NoError(t, err, "witness built for a fresh output must satisfy OutputCircuit")
}

func TestBuildOutputWitness_ReturnsSpendableNote(t *testing.T) {
	compileTestCircuits(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	wr, err := prover.BuildOutputWitness(500, "USD", testPP, tr[:])
	require.NoError(t, err)
	require.NotNil(t, wr.Note)

	// The returned Note must, when opened, reproduce the same commitment
	// published in the OutputCircuit assignment, otherwise this newly
	// created token would be unspendable by whoever receives the Note.
	commitment, err := wr.Note.Commitment(testPP.Curve)
	require.NoError(t, err)
	require.Equal(t, wr.Commitment, commitment)
}

func TestBuildOutputWitness_ZeroValueRejectedBySolver(t *testing.T) {
	compileTestCircuits(t)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	wr, err := prover.BuildOutputWitness(0, "USD", testPP, tr[:])
	require.NoError(t, err, "BuildOutputWitness itself does not enforce value > 0")

	witness, err := frontend.NewWitness(wr.Assignment, testPP.Curve.ScalarField())
	require.NoError(t, err)

	err = testOutputCS.IsSolved(witness)
	require.Error(t, err,
		"a zero-value output witness must fail OutputCircuit's range constraint, "+
			"confirming the circuit itself is the enforcement point, not the witness builder")
}

// ── BN254 witness tests ─────────────────────────────────────────────────────

func compileTestCircuitsBN254(t *testing.T) (*pp.PublicParams, constraint.ConstraintSystem, constraint.ConstraintSystem) {
	t.Helper()
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BN254

	spendCS, err := setup.CompileSpendCircuit(p)
	if err != nil {
		t.Fatalf("BN254 SpendCircuit compile: %v", err)
	}
	outputCS, err := setup.CompileOutputCircuit(p)
	if err != nil {
		t.Fatalf("BN254 OutputCircuit compile: %v", err)
	}

	return p, spendCS, outputCS
}

// randomBN254TypeRandomness returns type randomness bytes that are canonical
// for the BN254 scalar field (which is smaller than BLS12-381's field).
func randomBN254TypeRandomness(t *testing.T) []byte {
	t.Helper()
	var e bn254fr.Element
	_, err := e.SetRandom()
	require.NoError(t, err)
	b := e.Bytes()

	return b[:]
}

func TestBuildSpendWitness_BN254_Satisfiable(t *testing.T) {
	p, spendCS, _ := compileTestCircuitsBN254(t)

	note, err := snarktoken.NewRandomNote(500, "USD", p.Curve)
	require.NoError(t, err)

	tr := randomBN254TypeRandomness(t)

	witnessRes, err := prover.BuildSpendWitness(note, tr, p)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, p.Curve.ScalarField())
	require.NoError(t, err)

	err = spendCS.IsSolved(witness)
	require.NoError(t, err, "BN254 spend witness must satisfy SpendCircuit")
}

func TestBuildSpendWitness_BN254_CommitmentMatchesNote(t *testing.T) {
	p, _, _ := compileTestCircuitsBN254(t)

	note, err := snarktoken.NewRandomNote(500, "USD", p.Curve)
	require.NoError(t, err)

	tr := randomBN254TypeRandomness(t)

	witnessRes, err := prover.BuildSpendWitness(note, tr, p)
	require.NoError(t, err)

	commitment, err := note.Commitment(p.Curve)
	require.NoError(t, err)

	require.Equal(t, commitment, witnessRes.Commitment,
		"BN254 BuildSpendWitness commitment must match note.Commitment()")
}

func TestBuildOutputWitness_BN254_Satisfiable(t *testing.T) {
	p, _, outputCS := compileTestCircuitsBN254(t)

	tr := randomBN254TypeRandomness(t)

	witnessRes, err := prover.BuildOutputWitness(500, "USD", p, tr)
	require.NoError(t, err)

	witness, err := frontend.NewWitness(witnessRes.Assignment, p.Curve.ScalarField())
	require.NoError(t, err)

	err = outputCS.IsSolved(witness)
	require.NoError(t, err, "BN254 output witness must satisfy OutputCircuit")
}

func TestBuildOutputWitness_BN254_ReturnsSpendableNote(t *testing.T) {
	p, _, _ := compileTestCircuitsBN254(t)

	tr := randomBN254TypeRandomness(t)

	wr, err := prover.BuildOutputWitness(500, "USD", p, tr)
	require.NoError(t, err)
	require.NotNil(t, wr.Note)

	commitment, err := wr.Note.Commitment(p.Curve)
	require.NoError(t, err)
	require.Equal(t, wr.Commitment, commitment,
		"BN254 output note must reproduce the same commitment")
}

func TestBuildSpendWitness_UnsupportedCurve(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BW6_761 // unsupported

	note, err := snarktoken.NewRandomNote(100, "USD", testPP.Curve)
	require.NoError(t, err)

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	_, err = prover.BuildSpendWitness(note, tr[:], p)
	require.Error(t, err, "an unsupported curve must be rejected")
}

func TestBuildOutputWitness_UnsupportedCurve(t *testing.T) {
	p := pp.DefaultPublicParams()
	p.Curve = ecc.BW6_761

	var typeRandomness fr.Element
	_, _ = typeRandomness.SetRandom()
	tr := typeRandomness.Bytes()

	_, err := prover.BuildOutputWitness(100, "USD", p, tr[:])
	require.Error(t, err, "an unsupported curve must be rejected")
}

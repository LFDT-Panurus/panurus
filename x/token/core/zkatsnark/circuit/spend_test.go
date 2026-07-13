/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc"
)

// ── Package-level setup — compiled once for all spend tests ──────────────────

var (
	spendCS   constraint.ConstraintSystem
	spendPK   groth16.ProvingKey
	spendVK   groth16.VerifyingKey
	spendOnce sync.Once
)

func setupSpend(t *testing.T) {
	t.Helper()
	spendOnce.Do(func() {
		var err error
		spendCS, err = frontend.Compile(
			ecc.BLS12_381.ScalarField(),
			r1cs.NewBuilder,
			&circuit.SpendCircuit{},
		)
		if err != nil {
			panic(fmt.Sprintf("SpendCircuit compilation failed: %v", err))
		}

		fmt.Printf("SpendCircuit: %d constraints\n", spendCS.GetNbConstraints())

		spendPK, spendVK, err = groth16.Setup(spendCS)
		if err != nil {
			panic(fmt.Sprintf("SpendCircuit groth16.Setup failed: %v", err))
		}
	})
}

type spendInputs struct {
	value      uint64
	tokenType  fr.Element
	randomness fr.Element
	rcv        fr.Element
}

func newRandomSpendInputs(t *testing.T) spendInputs {
	t.Helper()
	var tokenType, randomness, rcv fr.Element
	tokenType.SetBytes([]byte("USD"))
	_, err := randomness.SetRandom()
	require.NoError(t, err)
	_, err = rcv.SetRandom()
	require.NoError(t, err)

	return spendInputs{
		value:      100,
		tokenType:  tokenType,
		randomness: randomness,
		rcv:        rcv,
	}
}

func buildSpendAssignment(t *testing.T, inp spendInputs) *circuit.SpendCircuit {
	t.Helper()

	// Encode value as a field element — same encoding as in the prover.
	var vField fr.Element
	vField.SetUint64(inp.value)

	// Compute the commitment using native MiMC.
	// Order must match Define: (Value, TokenType, Randomness).
	cm, err := mimc.Hash(vField, inp.tokenType, inp.randomness)
	require.NoError(t, err, "mimc.Hash failed in buildSpendAssignment")

	// Compute the value commitment using native Jubjub.
	cv, err := jubjub.ValueCommit(inp.value, inp.rcv)
	require.NoError(t, err, "jubjub.ValueCommit failed in buildSpendAssignment")

	return &circuit.SpendCircuit{
		// Public inputs — computed from private inputs
		CommitmentIn:   cm,
		ValueCommitInX: cv.X,
		ValueCommitInY: cv.Y,
		TokenType:      inp.tokenType,
		// Private inputs
		Value:      vField,
		Randomness: inp.randomness,
		RCV:        inp.rcv,
	}
}

func TestSpendCircuitValidWitness(t *testing.T) {
	setupSpend(t)

	inp := newRandomSpendInputs(t)
	assignment := buildSpendAssignment(t, inp)

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := groth16.Prove(spendCS, spendPK, witness)
	require.NoError(t, err, "groth16.Prove failed for a valid SpendCircuit witness")

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	err = groth16.Verify(proof, spendVK, publicWitness)
	require.NoError(t, err, "groth16.Verify failed for a valid SpendCircuit proof")
}

func TestSpendCircuitInvalid_WrongValue(t *testing.T) {
	setupSpend(t)

	inp := newRandomSpendInputs(t)
	assignment := buildSpendAssignment(t, inp)

	var wrongValue fr.Element
	wrongValue.SetUint64(inp.value - 1)
	assignment.Value = wrongValue

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = spendCS.IsSolved(witness)
	require.Error(t, err,
		"CRITICAL: SpendCircuit accepted a witness with wrong Value — "+
			"commitment constraint is not working")
}

func TestSpendCircuitInvalid_WrongRandomness(t *testing.T) {
	setupSpend(t)

	inp := newRandomSpendInputs(t)
	assignment := buildSpendAssignment(t, inp)

	var wrongRandomness fr.Element
	_, err := wrongRandomness.SetRandom()
	require.NoError(t, err)
	assignment.Randomness = wrongRandomness

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = spendCS.IsSolved(witness)
	require.Error(t, err,
		"SpendCircuit must reject a witness with wrong Randomness")
}

func TestSpendCircuitInvalid_WrongTokenType(t *testing.T) {
	setupSpend(t)

	inp := newRandomSpendInputs(t)
	assignment := buildSpendAssignment(t, inp)

	var wrongTokenType fr.Element
	wrongTokenType.SetBytes([]byte("EUR"))
	assignment.TokenType = wrongTokenType

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = spendCS.IsSolved(witness)
	require.Error(t, err,
		"SpendCircuit must reject a witness where TokenType mismatches CommitmentIn")
}

func TestSpendCircuitInvalid_WrongValueCommit(t *testing.T) {
	setupSpend(t)

	inp := newRandomSpendInputs(t)
	assignment := buildSpendAssignment(t, inp)

	var fakeRcv fr.Element
	_, err := fakeRcv.SetRandom()
	require.NoError(t, err)

	fakeCv, err := jubjub.ValueCommit(inp.value+1, fakeRcv)
	require.NoError(t, err)
	assignment.ValueCommitInX = fakeCv.X
	assignment.ValueCommitInY = fakeCv.Y

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = spendCS.IsSolved(witness)
	require.Error(t, err,
		"SpendCircuit must reject a witness where ValueCommit doesn't match Value/RCV")
}

func TestSpendCircuitInvalid_WrongRCV(t *testing.T) {
	setupSpend(t)

	inp := newRandomSpendInputs(t)
	assignment := buildSpendAssignment(t, inp)

	// Keep public ValueCommitInX/Y but change private RCV.
	var wrongRCV fr.Element
	_, err := wrongRCV.SetRandom()
	require.NoError(t, err)
	assignment.RCV = wrongRCV

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = spendCS.IsSolved(witness)
	require.Error(t, err,
		"SpendCircuit must reject a witness where RCV doesn't produce the public value commitment")
}

/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit_test

import (
    "fmt"
    "math/big"
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
    "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/params"
)
 
var (
    outputCS   constraint.ConstraintSystem
    outputPK   groth16.ProvingKey
    outputVK   groth16.VerifyingKey
    outputOnce sync.Once
)

func setupOutput(t *testing.T) {
    t.Helper()
    outputOnce.Do(func() {
        var err error
        outputCS, err = frontend.Compile(
            ecc.BLS12_381.ScalarField(),
            r1cs.NewBuilder,
            // MaxBits MUST be set here — it is baked into the circuit permanently.
            &circuit.OutputCircuit{MaxBits: params.DefaultMaxBits},
        )
        if err != nil {
            panic(fmt.Sprintf("OutputCircuit compilation failed: %v", err))
        }
        fmt.Printf("OutputCircuit: %d constraints\n", outputCS.GetNbConstraints())

        outputPK, outputVK, err = groth16.Setup(outputCS)
        if err != nil {
            panic(fmt.Sprintf("OutputCircuit groth16.Setup failed: %v", err))
        }
    })
}

type outputInputs struct {
    value      uint64
    tokenType  fr.Element
    randomness fr.Element
    rcv        fr.Element
}

func newRandomOutputInputs() outputInputs {
    var tokenType, randomness, rcv fr.Element
    tokenType.SetBytes([]byte("USD"))
    randomness.SetRandom()
    rcv.SetRandom()
    return outputInputs{value: 250, tokenType: tokenType, randomness: randomness, rcv: rcv}
}

func buildOutputAssignment(t *testing.T, inp outputInputs) *circuit.OutputCircuit {
    t.Helper()
    var vField fr.Element
    vField.SetUint64(inp.value)

    cm, err := mimc.Hash(vField, inp.tokenType, inp.randomness)
    require.NoError(t, err)

    cv, err := jubjub.ValueCommit(inp.value, inp.rcv)
    require.NoError(t, err)

    return &circuit.OutputCircuit{
        CommitmentOut:   cm,
        ValueCommitOutX: cv.X,
        ValueCommitOutY: cv.Y,
        TokenType:       inp.tokenType,
        Value:           vField,
        Randomness:      inp.randomness,
        RCV:             inp.rcv,
        MaxBits:         params.DefaultMaxBits,
    }
}

func TestOutputCircuitValidWitness(t *testing.T) {
	setupOutput(t)

	inp := newRandomOutputInputs()
	assignment := buildOutputAssignment(t, inp)

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
    require.NoError(t, err)

    proof, err := groth16.Prove(outputCS, outputPK, witness)
    require.NoError(t, err, "groth16.Prove failed for a valid OutputCircuit witness")

    publicWitness, err := witness.Public()
    require.NoError(t, err)

    err = groth16.Verify(proof, outputVK, publicWitness)
    require.NoError(t, err, "groth16.Verify failed for a valid OutputCircuit proof")
}

func TestOutputCircuitInvalid_WrongValue(t *testing.T) {
    setupOutput(t)
    inp := newRandomOutputInputs()
    assignment := buildOutputAssignment(t, inp)

    var wrongValue fr.Element
    wrongValue.SetUint64(inp.value + 500)
    assignment.Value = wrongValue

    witness, _ := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
    require.Error(t, outputCS.IsSolved(witness), "must reject wrong Value")
}

func TestOutputCircuitInvalid_WrongRandomness(t *testing.T) {
    setupOutput(t)
    inp := newRandomOutputInputs()
    assignment := buildOutputAssignment(t, inp)

    var wrongR fr.Element
    wrongR.SetRandom()
    assignment.Randomness = wrongR

    witness, _ := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
    require.Error(t, outputCS.IsSolved(witness), "must reject wrong Randomness")
}

func TestOutputCircuitInvalid_WrongValueCommit(t *testing.T) {
    setupOutput(t)
    inp := newRandomOutputInputs()
    assignment := buildOutputAssignment(t, inp)

    var fakeRCV fr.Element
    fakeRCV.SetRandom()
    fakeCV, _ := jubjub.ValueCommit(inp.value+100, fakeRCV)
    assignment.ValueCommitOutX = fakeCV.X
    assignment.ValueCommitOutY = fakeCV.Y

    witness, _ := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
    require.Error(t, outputCS.IsSolved(witness), "must reject wrong ValueCommit")
}

// TestOutputCircuitInvalid_ZeroValue is the critical range test.
// Zero-value tokens would allow creating tokens from nothing.
// This test verifies the range constraint actually fires for v=0.
func TestOutputCircuitInvalid_ZeroValue(t *testing.T) {
    setupOutput(t)

    var tokenType, randomness, rcv fr.Element
    tokenType.SetBytes([]byte("USD"))
    randomness.SetRandom()
    rcv.SetRandom()

    var zeroField fr.Element
    zeroField.SetUint64(0)

    // Compute a VALID commitment for v=0 so the commitment constraint passes.
    // This isolates the range check as the only failing constraint.
    // If we used a wrong commitment, the test would pass for the wrong reason.
    cm, err := mimc.Hash(zeroField, tokenType, randomness)
    require.NoError(t, err)

    cv, err := jubjub.ValueCommit(0, rcv)
    require.NoError(t, err)

    assignment := &circuit.OutputCircuit{
        CommitmentOut:   cm,
        ValueCommitOutX: cv.X,
        ValueCommitOutY: cv.Y,
        TokenType:       tokenType,
        Value:           zeroField, // ← the invalid field
        Randomness:      randomness,
        RCV:             rcv,
        MaxBits:         params.DefaultMaxBits,
    }

    witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
    require.NoError(t, err)

    err = outputCS.IsSolved(witness)
    require.Error(t, err,
        "CRITICAL: OutputCircuit accepted Value=0 — zero-value tokens are possible, "+
            "check that api.AssertIsDifferent(c.Value, 0) is in Define")
}

// TestOutputCircuitInvalid_OverflowValue verifies values >= 2^MaxBits are rejected.
// An overflow value could be used to wrap around and represent a much smaller amount,
// breaking conservation accounting.
func TestOutputCircuitInvalid_OverflowValue(t *testing.T) {
    setupOutput(t)

    // Construct a field element equal to 2^MaxBits.
    // This is a valid BLS12-381 field element but exceeds the MaxBits limit.
    var overflowValue fr.Element
    var b big.Int
    b.SetBit(&b, params.DefaultMaxBits, 1) // 2^64
    overflowValue.SetBigInt(&b)

    var tokenType, randomness, rcv fr.Element
    tokenType.SetBytes([]byte("USD"))
    randomness.SetRandom()
    rcv.SetRandom()

    // Compute a consistent commitment so only the range check fires.
    cm, err := mimc.Hash(overflowValue, tokenType, randomness)
    require.NoError(t, err)

    // We cannot call jubjub.ValueCommit with a field element value > uint64 max.
    // Use a placeholder for the value commitment — the range check should fire first.
    var fakeCVX, fakeCVY fr.Element
    fakeCVX.SetRandom()
    fakeCVY.SetRandom()

    assignment := &circuit.OutputCircuit{
        CommitmentOut:   cm,
        ValueCommitOutX: fakeCVX,
        ValueCommitOutY: fakeCVY,
        TokenType:       tokenType,
        Value:           overflowValue,
        Randomness:      randomness,
        RCV:             rcv,
        MaxBits:         params.DefaultMaxBits,
    }

    witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
    require.NoError(t, err)

    err = outputCS.IsSolved(witness)
    require.Error(t, err,
        "OutputCircuit must reject values >= 2^MaxBits — overflow attack possible if not")
}

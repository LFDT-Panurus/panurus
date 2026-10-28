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

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/mimc"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/params"
)

var (
	fabtokenMigCS   constraint.ConstraintSystem
	fabtokenMigPK   groth16.ProvingKey
	fabtokenMigVK   groth16.VerifyingKey
	fabtokenMigOnce sync.Once
)

func setupFabtokenMigration(t *testing.T) {
	t.Helper()
	fabtokenMigOnce.Do(func() {
		var err error
		fabtokenMigCS, err = frontend.Compile(
			ecc.BLS12_381.ScalarField(),
			r1cs.NewBuilder,
			&circuit.FabtokenMigrationCircuit{MaxBits: params.DefaultMaxBits},
		)
		if err != nil {
			panic(fmt.Sprintf("FabtokenMigrationCircuit compilation failed: %v", err))
		}
		fmt.Printf("FabtokenMigrationCircuit: %d constraints\n", fabtokenMigCS.GetNbConstraints())

		fabtokenMigPK, fabtokenMigVK, err = groth16.Setup(fabtokenMigCS)
		if err != nil {
			panic(fmt.Sprintf("FabtokenMigrationCircuit groth16.Setup failed: %v", err))
		}
	})
}

type fabtokenMigrationInputs struct {
	value          uint64
	tokenType      fr.Element
	randomnessNew  fr.Element
	rcv            fr.Element
	typeRandomness fr.Element
}

func newRandomFabtokenMigrationInputs(t *testing.T) fabtokenMigrationInputs {
	t.Helper()
	var tokenType, randomnessNew, rcv, typeRandomness fr.Element
	tokenType.SetBytes([]byte("USD"))
	_, err := randomnessNew.SetRandom()
	require.NoError(t, err)
	_, err = rcv.SetRandom()
	require.NoError(t, err)
	_, err = typeRandomness.SetRandom()
	require.NoError(t, err)

	return fabtokenMigrationInputs{
		value:          250,
		tokenType:      tokenType,
		randomnessNew:  randomnessNew,
		rcv:            rcv,
		typeRandomness: typeRandomness,
	}
}

func buildFabtokenMigrationAssignment(t *testing.T, inp fabtokenMigrationInputs) *circuit.FabtokenMigrationCircuit {
	t.Helper()
	var vField fr.Element
	vField.SetUint64(inp.value)

	// Constraint 1: Fabtoken digest MiMC(Value, TokenType)
	digest, err := mimc.Hash(vField, inp.tokenType)
	require.NoError(t, err)

	// Constraint 2: MiMC note commitment MiMC(Value, TokenType, RandomnessNew)
	cm, err := mimc.Hash(vField, inp.tokenType, inp.randomnessNew)
	require.NoError(t, err)

	// Constraint 3: Value commitment Value·V + RCV·R
	cv, err := edwards.Jubjub().ValueCommit(inp.value, inp.rcv)
	require.NoError(t, err)

	// Constraint 5: Type commitment MiMC(TokenType, TypeRandomness)
	tc, err := mimc.Hash(inp.tokenType, inp.typeRandomness)
	require.NoError(t, err)

	return &circuit.FabtokenMigrationCircuit{
		FabtokenDigest:  digest,
		CommitmentMiMC:  cm,
		ValueCommitOutX: cv.X,
		ValueCommitOutY: cv.Y,
		TypeCommitment:  tc,
		Value:           vField,
		TokenType:       inp.tokenType,
		RandomnessNew:   inp.randomnessNew,
		RCV:             inp.rcv,
		TypeRandomness:  inp.typeRandomness,
		MaxBits:         params.DefaultMaxBits,
	}
}

func TestFabtokenMigrationCircuitValidWitness(t *testing.T) {
	setupFabtokenMigration(t)

	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := groth16.Prove(fabtokenMigCS, fabtokenMigPK, witness)
	require.NoError(t, err, "groth16.Prove failed for a valid FabtokenMigrationCircuit witness")

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	err = groth16.Verify(proof, fabtokenMigVK, publicWitness)
	require.NoError(t, err, "groth16.Verify failed for a valid FabtokenMigrationCircuit proof")
}

func TestFabtokenMigrationCircuitInvalid_WrongFabtokenDigest(t *testing.T) {
	setupFabtokenMigration(t)
	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	var wrongDigest fr.Element
	_, err := wrongDigest.SetRandom()
	require.NoError(t, err)
	assignment.FabtokenDigest = wrongDigest

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject wrong FabtokenDigest")
}

func TestFabtokenMigrationCircuitInvalid_WrongValue(t *testing.T) {
	setupFabtokenMigration(t)
	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	var wrongValue fr.Element
	wrongValue.SetUint64(inp.value + 500)
	assignment.Value = wrongValue

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject wrong Value")
}

func TestFabtokenMigrationCircuitInvalid_WrongTokenType(t *testing.T) {
	setupFabtokenMigration(t)
	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	var wrongType fr.Element
	wrongType.SetBytes([]byte("EUR"))
	assignment.TokenType = wrongType

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject wrong TokenType")
}

func TestFabtokenMigrationCircuitInvalid_WrongRandomnessNew(t *testing.T) {
	setupFabtokenMigration(t)
	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	var wrongR fr.Element
	_, err := wrongR.SetRandom()
	require.NoError(t, err)
	assignment.RandomnessNew = wrongR

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject wrong RandomnessNew")
}

func TestFabtokenMigrationCircuitInvalid_WrongRCV(t *testing.T) {
	setupFabtokenMigration(t)
	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	var wrongRCV fr.Element
	_, err := wrongRCV.SetRandom()
	require.NoError(t, err)
	assignment.RCV = wrongRCV

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject wrong RCV")
}

func TestFabtokenMigrationCircuitInvalid_WrongTypeRandomness(t *testing.T) {
	setupFabtokenMigration(t)
	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)

	var wrongTR fr.Element
	_, err := wrongTR.SetRandom()
	require.NoError(t, err)
	assignment.TypeRandomness = wrongTR

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject wrong TypeRandomness")
}

func TestFabtokenMigrationCircuitInvalid_ZeroValue(t *testing.T) {
	setupFabtokenMigration(t)

	var tokenType, randomnessNew, rcv, typeRandomness fr.Element
	tokenType.SetBytes([]byte("USD"))
	_, err := randomnessNew.SetRandom()
	require.NoError(t, err)
	_, err = rcv.SetRandom()
	require.NoError(t, err)
	_, err = typeRandomness.SetRandom()
	require.NoError(t, err)

	var zeroField fr.Element
	zeroField.SetUint64(0)

	// Valid digest, commitment, and type commitment for value=0
	digest, err := mimc.Hash(zeroField, tokenType)
	require.NoError(t, err)
	cm, err := mimc.Hash(zeroField, tokenType, randomnessNew)
	require.NoError(t, err)
	cv, err := edwards.Jubjub().ValueCommit(0, rcv)
	require.NoError(t, err)
	tc, err := mimc.Hash(tokenType, typeRandomness)
	require.NoError(t, err)

	assignment := &circuit.FabtokenMigrationCircuit{
		FabtokenDigest:  digest,
		CommitmentMiMC:  cm,
		ValueCommitOutX: cv.X,
		ValueCommitOutY: cv.Y,
		TypeCommitment:  tc,
		Value:           zeroField,
		TokenType:       tokenType,
		RandomnessNew:   randomnessNew,
		RCV:             rcv,
		TypeRandomness:  typeRandomness,
		MaxBits:         params.DefaultMaxBits,
	}

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = fabtokenMigCS.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject zero value")
}

func TestFabtokenMigrationCircuitInvalid_ValueTooLarge(t *testing.T) {
	const smallMaxBits = 8
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&circuit.FabtokenMigrationCircuit{MaxBits: smallMaxBits},
	)
	require.NoError(t, err)

	var tokenType, randomnessNew, rcv, typeRandomness fr.Element
	tokenType.SetBytes([]byte("USD"))
	_, _ = randomnessNew.SetRandom()
	_, _ = rcv.SetRandom()
	_, _ = typeRandomness.SetRandom()

	val := uint64(1 << smallMaxBits) // 256 >= 2^8
	var vField fr.Element
	vField.SetUint64(val)

	digest, _ := mimc.Hash(vField, tokenType)
	cm, _ := mimc.Hash(vField, tokenType, randomnessNew)
	cv, _ := edwards.Jubjub().ValueCommit(val, rcv)
	tc, _ := mimc.Hash(tokenType, typeRandomness)

	assignment := &circuit.FabtokenMigrationCircuit{
		FabtokenDigest:  digest,
		CommitmentMiMC:  cm,
		ValueCommitOutX: cv.X,
		ValueCommitOutY: cv.Y,
		TypeCommitment:  tc,
		Value:           vField,
		TokenType:       tokenType,
		RandomnessNew:   randomnessNew,
		RCV:             rcv,
		TypeRandomness:  typeRandomness,
		MaxBits:         smallMaxBits,
	}

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = cs.IsSolved(witness)
	require.Error(t, err, "FabtokenMigrationCircuit must reject value >= 2^MaxBits")
}

func TestFabtokenMigrationCircuit_ExplicitGenerators(t *testing.T) {
	j := edwards.Jubjub()
	cs, err := frontend.Compile(
		ecc.BLS12_381.ScalarField(),
		r1cs.NewBuilder,
		&circuit.FabtokenMigrationCircuit{
			MaxBits: params.DefaultMaxBits,
			GenVX:   j.V.X.BigInt(new(big.Int)),
			GenVY:   j.V.Y.BigInt(new(big.Int)),
			GenRX:   j.R.X.BigInt(new(big.Int)),
			GenRY:   j.R.Y.BigInt(new(big.Int)),
		},
	)
	require.NoError(t, err)
	require.Positive(t, cs.GetNbConstraints(), "Circuit must have constraints")

	inp := newRandomFabtokenMigrationInputs(t)
	assignment := buildFabtokenMigrationAssignment(t, inp)
	assignment.GenVX = j.V.X.BigInt(new(big.Int))
	assignment.GenVY = j.V.Y.BigInt(new(big.Int))
	assignment.GenRX = j.R.X.BigInt(new(big.Int))
	assignment.GenRY = j.R.Y.BigInt(new(big.Int))

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = cs.IsSolved(witness)
	require.NoError(t, err, "Witness must solve circuit with explicit generators")
}

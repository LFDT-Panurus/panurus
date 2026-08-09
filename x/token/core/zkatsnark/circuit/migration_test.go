/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit_test

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"testing"

	mathlib "github.com/IBM/mathlib"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/stretchr/testify/require"

	"github.com/consensys/gnark-crypto/ecc"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/jubjub"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/params"
)

var (
	migrationCS   constraint.ConstraintSystem
	migrationPK   groth16.ProvingKey
	migrationVK   groth16.VerifyingKey
	migrationOnce sync.Once

	// testPedGens holds the Pedersen generator coordinates extracted once.
	testPedGens pedGenerators
)

type pedGenerators struct {
	G0X, G0Y *big.Int
	G1X, G1Y *big.Int
	G2X, G2Y *big.Int
}

func deriveTestPedGenerators() pedGenerators {
	curve := mathlib.Curves[mathlib.BLS12_381]
	driverName := "zkatdlognogh"
	driverVersion := 1

	coords := make([]struct{ X, Y *big.Int }, 3)
	for i := range 3 {
		seed := "lfdt-panurus." + driverName + "." + strconv.Itoa(driverVersion) + ".PedersenGenerators." + strconv.Itoa(i)
		g := curve.HashToG1([]byte(seed))
		raw := g.Bytes()

		var pt bls12381.G1Affine
		if _, err := pt.SetBytes(raw); err != nil {
			panic("test: failed to parse Pedersen generator " + strconv.Itoa(i) + ": " + err.Error())
		}

		coords[i].X = pt.X.BigInt(new(big.Int))
		coords[i].Y = pt.Y.BigInt(new(big.Int))
	}

	return pedGenerators{
		G0X: coords[0].X, G0Y: coords[0].Y,
		G1X: coords[1].X, G1Y: coords[1].Y,
		G2X: coords[2].X, G2Y: coords[2].Y,
	}
}

func setupMigration(t *testing.T) {
	t.Helper()
	migrationOnce.Do(func() {
		testPedGens = deriveTestPedGenerators()

		var err error
		migrationCS, err = frontend.Compile(
			ecc.BLS12_381.ScalarField(),
			r1cs.NewBuilder,
			&circuit.MigrationCircuit{
				MaxBits: params.DefaultMaxBits,
				PedG0X:  testPedGens.G0X, PedG0Y: testPedGens.G0Y,
				PedG1X: testPedGens.G1X, PedG1Y: testPedGens.G1Y,
				PedG2X: testPedGens.G2X, PedG2Y: testPedGens.G2Y,
			},
		)
		if err != nil {
			panic(fmt.Sprintf("MigrationCircuit compilation failed: %v", err))
		}

		fmt.Printf("MigrationCircuit: %d constraints\n", migrationCS.GetNbConstraints())

		migrationPK, migrationVK, err = groth16.Setup(migrationCS)
		if err != nil {
			panic(fmt.Sprintf("MigrationCircuit groth16.Setup failed: %v", err))
		}
	})
}

// ── Test inputs ─────────────────────────────────────────────────────────────

type migrationInputs struct {
	value          uint64
	tokenType      string
	tokenTypeFr    fr.Element // EncodeTokenType(tokenType)
	tokenTypePed   fr.Element // HashToZr(tokenType) for Pedersen
	randomnessPed  fr.Element // Pedersen blinding factor
	randomnessNew  fr.Element // MiMC randomness
	rcv            fr.Element // Jubjub value-commitment randomness
	typeRandomness fr.Element // TypeCommitment randomness
}

func newRandomMigrationInputs(t *testing.T) migrationInputs {
	t.Helper()

	tokenType := "USD"
	value := uint64(100)

	// EncodeTokenType, same as snarktoken.EncodeTokenType
	var tokenTypeFr fr.Element
	tokenTypeFr.SetBytes([]byte(tokenType))

	digest := sha256.Sum256([]byte(tokenType))
	digestBig := new(big.Int).SetBytes(digest[:])
	digestBig.Mod(digestBig, fr.Modulus())
	var tokenTypePed fr.Element
	tokenTypePed.SetBigInt(digestBig)

	var randomnessPed, randomnessNew, rcv, typeRandomness fr.Element
	_, err := randomnessPed.SetRandom()
	require.NoError(t, err)
	_, err = randomnessNew.SetRandom()
	require.NoError(t, err)
	_, err = rcv.SetRandom()
	require.NoError(t, err)
	_, err = typeRandomness.SetRandom()
	require.NoError(t, err)

	return migrationInputs{
		value:          value,
		tokenType:      tokenType,
		tokenTypeFr:    tokenTypeFr,
		tokenTypePed:   tokenTypePed,
		randomnessPed:  randomnessPed,
		randomnessNew:  randomnessNew,
		rcv:            rcv,
		typeRandomness: typeRandomness,
	}
}

// computePedersenCommitment replicates the zkatdlog commit() function:
// C = tokenTypePed·G[0] + value·G[1] + blindingFactor·G[2]
func computePedersenCommitment(t *testing.T, inp migrationInputs) bls12381.G1Affine {
	t.Helper()

	curve := mathlib.Curves[mathlib.BLS12_381]
	driverName := "zkatdlognogh"
	driverVersion := 1

	gens := make([]*mathlib.G1, 3)
	for i := range 3 {
		seed := "lfdt-panurus." + driverName + "." + strconv.Itoa(driverVersion) + ".PedersenGenerators." + strconv.Itoa(i)
		gens[i] = curve.HashToG1([]byte(seed))
	}

	// Build scalar vector: [tokenTypePed, value, blindingFactor]
	typePedBytes := inp.tokenTypePed.Marshal()
	typeScalar := curve.NewZrFromBytes(typePedBytes)

	var vField fr.Element
	vField.SetUint64(inp.value)
	vBytes := vField.Marshal()
	valueScalar := curve.NewZrFromBytes(vBytes)

	bfBytes := inp.randomnessPed.Marshal()
	bfScalar := curve.NewZrFromBytes(bfBytes)

	// Compute C = sum(scalar_i * G_i)
	com := curve.NewG1()
	com.Add(gens[0].Mul(typeScalar))
	com.Add(gens[1].Mul(valueScalar))
	com.Add(gens[2].Mul(bfScalar))

	// Convert to gnark-crypto G1Affine
	raw := com.Bytes()
	var pt bls12381.G1Affine
	_, err := pt.SetBytes(raw)
	require.NoError(t, err, "failed to parse computed Pedersen commitment")

	return pt
}

func buildMigrationAssignment(t *testing.T, inp migrationInputs) *circuit.MigrationCircuit {
	t.Helper()

	// Compute Pedersen commitment
	pedCommit := computePedersenCommitment(t, inp)
	pedXBig := pedCommit.X.BigInt(new(big.Int))
	pedYBig := pedCommit.Y.BigInt(new(big.Int))

	// Compute MiMC commitment: MiMC(value, tokenTypeFr, randomnessNew)
	var vField fr.Element
	vField.SetUint64(inp.value)
	cm, err := mimc.Hash(vField, inp.tokenTypeFr, inp.randomnessNew)
	require.NoError(t, err, "mimc.Hash failed")

	// Compute Jubjub value commitment
	cv, err := jubjub.ValueCommit(inp.value, inp.rcv)
	require.NoError(t, err, "jubjub.ValueCommit failed")

	// Compute type commitment: MiMC(TokenType, TypeRandomness)
	tc, err := mimc.Hash(inp.tokenTypeFr, inp.typeRandomness)
	require.NoError(t, err, "mimc.Hash failed for type commitment")

	return &circuit.MigrationCircuit{
		// Public inputs
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](pedXBig),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](pedYBig),
		CommitmentMiMC:      cm,
		ValueCommitOutX:     cv.X,
		ValueCommitOutY:     cv.Y,
		TypeCommitment:      tc,
		// Private inputs
		Value:          vField,
		TokenType:      inp.tokenTypeFr,
		TokenTypePed:   inp.tokenTypePed,
		RandomnessPed:  inp.randomnessPed,
		RandomnessNew:  inp.randomnessNew,
		RCV:            inp.rcv,
		TypeRandomness: inp.typeRandomness,
		// Compile-time parameters
		MaxBits: params.DefaultMaxBits,
		PedG0X:  testPedGens.G0X, PedG0Y: testPedGens.G0Y,
		PedG1X: testPedGens.G1X, PedG1Y: testPedGens.G1Y,
		PedG2X: testPedGens.G2X, PedG2Y: testPedGens.G2Y,
	}
}

func TestMigrationCircuitValidWitness(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := groth16.Prove(migrationCS, migrationPK, witness)
	require.NoError(t, err, "groth16.Prove failed for a valid MigrationCircuit witness")

	publicWitness, err := witness.Public()
	require.NoError(t, err)

	err = groth16.Verify(proof, migrationVK, publicWitness)
	require.NoError(t, err, "groth16.Verify failed for a valid MigrationCircuit proof")
}

func TestMigrationCircuitInvalid_WrongValue(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	// Tamper with the private Value — should break BOTH Pedersen AND MiMC
	var wrongValue fr.Element
	wrongValue.SetUint64(inp.value + 1)
	assignment.Value = wrongValue

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err,
		"CRITICAL: MigrationCircuit accepted a witness with wrong Value — "+
			"both Pedersen AND MiMC constraints should fail")
}

func TestMigrationCircuitInvalid_WrongRandomnessNew(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	var wrongR fr.Element
	_, err := wrongR.SetRandom()
	require.NoError(t, err)
	assignment.RandomnessNew = wrongR

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err, "MigrationCircuit must reject a witness with wrong MiMC randomness")
}

func TestMigrationCircuitInvalid_WrongRandomnessPed(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	var wrongBF fr.Element
	_, err := wrongBF.SetRandom()
	require.NoError(t, err)
	assignment.RandomnessPed = wrongBF

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err, "MigrationCircuit must reject a witness with wrong Pedersen blinding factor")
}

func TestMigrationCircuitInvalid_WrongTokenTypePed(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	// Use a different token type hash — breaks Pedersen opening.
	digest := sha256.Sum256([]byte("EUR"))
	digestBig := new(big.Int).SetBytes(digest[:])
	digestBig.Mod(digestBig, fr.Modulus())
	var wrongTypePed fr.Element
	wrongTypePed.SetBigInt(digestBig)
	assignment.TokenTypePed = wrongTypePed

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err,
		"MigrationCircuit must reject a witness where TokenTypePed doesn't match the Pedersen commitment")
}

func TestMigrationCircuitInvalid_WrongRCV(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	var wrongRCV fr.Element
	_, err := wrongRCV.SetRandom()
	require.NoError(t, err)
	assignment.RCV = wrongRCV

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err,
		"MigrationCircuit must reject a witness where RCV doesn't produce the public value commitment")
}

func TestMigrationCircuitInvalid_WrongValueCommit(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	// Replace public value commitment with a random one.
	var fakeRCV fr.Element
	_, err := fakeRCV.SetRandom()
	require.NoError(t, err)
	fakeCV, err := jubjub.ValueCommit(inp.value+50, fakeRCV)
	require.NoError(t, err)
	assignment.ValueCommitOutX = fakeCV.X
	assignment.ValueCommitOutY = fakeCV.Y

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err, "MigrationCircuit must reject a witness with wrong ValueCommit")
}

func TestMigrationCircuitInvalid_ZeroValue(t *testing.T) {
	setupMigration(t)

	// Build an internally-consistent witness for value=0.
	inp := newRandomMigrationInputs(t)
	inp.value = 0

	assignment := buildMigrationAssignment(t, inp)

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err,
		"CRITICAL: MigrationCircuit accepted Value=0 — zero-value tokens should be impossible")
}

func TestMigrationCircuitInvalid_OverflowValue(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)

	// Build assignment with value = 2^MaxBits (exceeds range).
	var overflowValue fr.Element
	var b big.Int
	b.SetBit(&b, params.DefaultMaxBits, 1)
	overflowValue.SetBigInt(&b)

	// Compute Pedersen commitment with the overflow value.
	pedCommit := computePedersenCommitment(t, migrationInputs{
		value:         0, // won't be used — overridden below
		tokenType:     inp.tokenType,
		tokenTypePed:  inp.tokenTypePed,
		randomnessPed: inp.randomnessPed,
	})

	// Compute MiMC commitment with the overflow value.
	cm, err := mimc.Hash(overflowValue, inp.tokenTypeFr, inp.randomnessNew)
	require.NoError(t, err)

	// Compute type commitment
	tc, err := mimc.Hash(inp.tokenTypeFr, inp.typeRandomness)
	require.NoError(t, err)

	pedXBig := pedCommit.X.BigInt(new(big.Int))
	pedYBig := pedCommit.Y.BigInt(new(big.Int))

	// Use random value commitment, range check should fire first.
	var fakeCVX, fakeCVY fr.Element
	_, err = fakeCVX.SetRandom()
	require.NoError(t, err)
	_, err = fakeCVY.SetRandom()
	require.NoError(t, err)

	assignment := &circuit.MigrationCircuit{
		CommitmentPedersenX: emulated.ValueOf[emulated.BLS12381Fp](pedXBig),
		CommitmentPedersenY: emulated.ValueOf[emulated.BLS12381Fp](pedYBig),
		CommitmentMiMC:      cm,
		ValueCommitOutX:     fakeCVX,
		ValueCommitOutY:     fakeCVY,
		TypeCommitment:      tc,
		Value:               overflowValue,
		TokenType:           inp.tokenTypeFr,
		TokenTypePed:        inp.tokenTypePed,
		RandomnessPed:       inp.randomnessPed,
		RandomnessNew:       inp.randomnessNew,
		RCV:                 inp.rcv,
		TypeRandomness:      inp.typeRandomness,
		MaxBits:             params.DefaultMaxBits,
		PedG0X:              testPedGens.G0X, PedG0Y: testPedGens.G0Y,
		PedG1X: testPedGens.G1X, PedG1Y: testPedGens.G1Y,
		PedG2X: testPedGens.G2X, PedG2Y: testPedGens.G2Y,
	}

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err,
		"MigrationCircuit must reject values >= 2^MaxBits, overflow attack possible if not")
}

func TestMigrationCircuitInvalid_WrongTypeRandomness(t *testing.T) {
	setupMigration(t)

	inp := newRandomMigrationInputs(t)
	assignment := buildMigrationAssignment(t, inp)

	var wrongTR fr.Element
	_, err := wrongTR.SetRandom()
	require.NoError(t, err)
	assignment.TypeRandomness = wrongTR

	witness, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	err = migrationCS.IsSolved(witness)
	require.Error(t, err,
		"MigrationCircuit must reject a witness where TypeRandomness doesn't produce the public TypeCommitment")
}

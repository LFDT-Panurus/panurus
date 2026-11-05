/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package circuit_test

import (
	"crypto/sha256"
	"math/big"
	"sync"
	"testing"

	mathlib "github.com/IBM/mathlib"
	"github.com/consensys/gnark-crypto/ecc"
	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/std/math/emulated"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/circuit"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/edwards"
)

// migrationTestMaxBits keeps the range check small, so that the overflow case
// is cheap to build; the circuit shape does not otherwise depend on it.
const migrationTestMaxBits = 16

var (
	migrationCS   constraint.ConstraintSystem
	migrationErr  error
	migrationOnce sync.Once

	// testPedGens are the Pedersen generators of a ZKAT-DLOG deployment on
	// BLS12_381_BBS_GURVY, the curve ZKAT-DLOG uses on BLS12-381 in NWO: its
	// HashToG1 differs from the plain BLS12_381 one, so these are not the
	// generators the former hard-coded derivation produced.
	testPedGens [3]*mathlib.G1
)

// bbsGenerators derives three independent generators with the BBS curve's
// hash-to-curve, as ZKAT-DLOG's GeneratePedersenParameters does.
func bbsGenerators() [3]*mathlib.G1 {
	c := mathlib.Curves[mathlib.BLS12_381_BBS_GURVY]
	var gens [3]*mathlib.G1
	for i, seed := range []string{"circuit-test-G0", "circuit-test-G1", "circuit-test-G2"} {
		gens[i] = c.HashToG1([]byte(seed))
	}

	return gens
}

func affine(t *testing.T, g *mathlib.G1) bls12381.G1Affine {
	t.Helper()
	var pt bls12381.G1Affine
	_, err := pt.SetBytes(g.Bytes())
	require.NoError(t, err)

	return pt
}

func setupMigration(t *testing.T) {
	t.Helper()
	migrationOnce.Do(func() {
		testPedGens = bbsGenerators()
		migrationCS, migrationErr = frontend.Compile(
			ecc.BLS12_381.ScalarField(),
			r1cs.NewBuilder,
			newMigrationCircuitTemplate(t),
		)
	})
	require.NoError(t, migrationErr)
}

func newMigrationCircuitTemplate(t *testing.T) *circuit.MigrationCircuit {
	t.Helper()
	g0, g1, g2 := affine(t, testPedGens[0]), affine(t, testPedGens[1]), affine(t, testPedGens[2])
	j := edwards.Jubjub()

	return &circuit.MigrationCircuit{
		MaxBits: migrationTestMaxBits,
		PedG0X:  g0.X.BigInt(new(big.Int)), PedG0Y: g0.Y.BigInt(new(big.Int)),
		PedG1X: g1.X.BigInt(new(big.Int)), PedG1Y: g1.Y.BigInt(new(big.Int)),
		PedG2X: g2.X.BigInt(new(big.Int)), PedG2Y: g2.Y.BigInt(new(big.Int)),
		GenVX: j.V.X.BigInt(new(big.Int)), GenVY: j.V.Y.BigInt(new(big.Int)),
		GenRX: j.R.X.BigInt(new(big.Int)), GenRY: j.R.Y.BigInt(new(big.Int)),
	}
}

// typePed is SHA-256(t) mod r, the ZKAT-DLOG type scalar.
func typePed(tokenType string) fr.Element {
	digest := sha256.Sum256([]byte(tokenType))
	var e fr.Element
	e.SetBigInt(new(big.Int).Mod(new(big.Int).SetBytes(digest[:]), fr.Modulus()))

	return e
}

type migrationInputs struct {
	value         *big.Int
	tokenType     string
	randomnessPed fr.Element
	rcv           fr.Element
}

func newMigrationInputs(t *testing.T, tokenType string, value uint64) migrationInputs {
	t.Helper()
	var bf, rcv fr.Element
	_, err := bf.SetRandom()
	require.NoError(t, err)
	_, err = rcv.SetRandom()
	require.NoError(t, err)

	return migrationInputs{value: new(big.Int).SetUint64(value), tokenType: tokenType, randomnessPed: bf, rcv: rcv}
}

// pedersen computes the ZKAT-DLOG commitment t_ped·G0 + v·G1 + bf·G2.
func pedersen(t *testing.T, tokenType string, value *big.Int, bf fr.Element) bls12381.G1Affine {
	t.Helper()
	c := mathlib.Curves[mathlib.BLS12_381_BBS_GURVY]
	tp := typePed(tokenType)
	var v fr.Element
	v.SetBigInt(value)
	com := c.NewG1()
	for i, s := range []fr.Element{tp, v, bf} {
		b := s.Bytes()
		com.Add(testPedGens[i].Mul(c.NewZrFromBytes(b[:])))
	}

	return affine(t, com)
}

// assignment builds a full witness for inp, with the public TokenTypePed of
// publicType.
func assignment(t *testing.T, inp migrationInputs, publicType string) *circuit.MigrationCircuit {
	t.Helper()
	ped := pedersen(t, inp.tokenType, inp.value, inp.randomnessPed)
	var rcvInt big.Int
	inp.rcv.BigInt(&rcvInt)
	cvX, cvY := valueCommit(t, inp.value, inp.rcv)

	a := newMigrationCircuitTemplate(t)
	a.CommitmentPedersenX = emulated.ValueOf[emulated.BLS12381Fp](ped.X.BigInt(new(big.Int)))
	a.CommitmentPedersenY = emulated.ValueOf[emulated.BLS12381Fp](ped.Y.BigInt(new(big.Int)))
	a.TokenTypePed = typePed(publicType)
	a.ValueCommitInX = cvX
	a.ValueCommitInY = cvY
	a.Value = inp.value
	a.RandomnessPed = inp.randomnessPed
	a.RCV = inp.rcv

	return a
}

// valueCommit computes v·V + rcv·R on Jubjub, also for values that do not
// fit in a uint64.
func valueCommit(t *testing.T, value *big.Int, rcv fr.Element) (*big.Int, *big.Int) {
	t.Helper()
	if value.IsUint64() {
		cv, err := edwards.Jubjub().ValueCommit(value.Uint64(), rcv)
		require.NoError(t, err)

		return cv.X.BigInt(new(big.Int)), cv.Y.BigInt(new(big.Int))
	}
	// out-of-range values are rejected by the range check before the value
	// commitment matters: any point will do
	cv, err := edwards.Jubjub().ValueCommit(1, rcv)
	require.NoError(t, err)

	return cv.X.BigInt(new(big.Int)), cv.Y.BigInt(new(big.Int))
}

func isSolved(t *testing.T, a *circuit.MigrationCircuit) error {
	t.Helper()
	w, err := frontend.NewWitness(a, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	return migrationCS.IsSolved(w)
}

func TestMigrationCircuit_ValidWitness(t *testing.T) {
	setupMigration(t)
	require.NoError(t, isSolved(t, assignment(t, newMigrationInputs(t, "USD", 100), "USD")))
	require.NoError(t, isSolved(t, assignment(t, newMigrationInputs(t, "USD", 1<<migrationTestMaxBits-1), "USD")), "maximum value")
}

// TestMigrationCircuit_TypeSwap is the regression test for the type-swap
// defect: the honest opening of a "USD" commitment must not prove a public
// type scalar of "EUR", so a holder of USD cannot mint EUR.
func TestMigrationCircuit_TypeSwap(t *testing.T) {
	setupMigration(t)
	require.Error(t, isSolved(t, assignment(t, newMigrationInputs(t, "USD", 100), "EUR")))
}

func TestMigrationCircuit_Rejections(t *testing.T) {
	setupMigration(t)
	random := func(t *testing.T) fr.Element {
		t.Helper()
		var e fr.Element
		_, err := e.SetRandom()
		require.NoError(t, err)

		return e
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, a *circuit.MigrationCircuit)
	}{
		{"wrong value", func(_ *testing.T, a *circuit.MigrationCircuit) { a.Value = big.NewInt(101) }},
		{"wrong Pedersen randomness", func(t *testing.T, a *circuit.MigrationCircuit) {
			t.Helper()
			a.RandomnessPed = random(t)
		}},
		{"wrong RCV", func(t *testing.T, a *circuit.MigrationCircuit) {
			t.Helper()
			a.RCV = random(t)
		}},
		{"value commitment of another value", func(t *testing.T, a *circuit.MigrationCircuit) {
			t.Helper()
			x, y := valueCommit(t, big.NewInt(150), random(t))
			a.ValueCommitInX, a.ValueCommitInY = x, y
		}},
		{"Pedersen point of another token", func(t *testing.T, a *circuit.MigrationCircuit) {
			t.Helper()
			other := pedersen(t, "USD", big.NewInt(100), random(t))
			a.CommitmentPedersenX = emulated.ValueOf[emulated.BLS12381Fp](other.X.BigInt(new(big.Int)))
			a.CommitmentPedersenY = emulated.ValueOf[emulated.BLS12381Fp](other.Y.BigInt(new(big.Int)))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := assignment(t, newMigrationInputs(t, "USD", 100), "USD")
			tc.mutate(t, a)
			require.Error(t, isSolved(t, a))
		})
	}
}

// TestMigrationCircuit_RangeCheck checks that a commitment to 0 or to a value
// of MaxBits bits or more cannot be redeemed, even with an otherwise honest
// opening.
func TestMigrationCircuit_RangeCheck(t *testing.T) {
	setupMigration(t)
	t.Run("zero", func(t *testing.T) {
		require.Error(t, isSolved(t, assignment(t, newMigrationInputs(t, "USD", 0), "USD")))
	})
	t.Run("overflow", func(t *testing.T) {
		inp := newMigrationInputs(t, "USD", 0)
		inp.value = new(big.Int).Lsh(big.NewInt(1), migrationTestMaxBits)
		require.Error(t, isSolved(t, assignment(t, inp, "USD")))
	})
}

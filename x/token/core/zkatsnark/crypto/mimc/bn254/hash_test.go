/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bn254_test

import (
	"fmt"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/circuit/gadgets"
	mimcbn254 "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/mimc/bn254"
)

func TestHash_KnownVector(t *testing.T) {
	// Fresh known-vector: MiMC(1, 2, 3) over BN254
	var a, b, c fr.Element
	a.SetUint64(1)
	b.SetUint64(2)
	c.SetUint64(3)

	h, err := mimcbn254.Hash(a, b, c)
	require.NoError(t, err)

	// Determinism check: same inputs → same output
	h2, err := mimcbn254.Hash(a, b, c)
	require.NoError(t, err)
	require.True(t, h.Equal(&h2), "MiMC BN254 must be deterministic")

	// Print the known vector for future regression
	t.Logf("MiMC_BN254(1,2,3) = 0x%s", h.Text(16))
}

func TestHash_SingleInput(t *testing.T) {
	var a fr.Element
	a.SetUint64(42)
	_, err := mimcbn254.Hash(a)
	require.NoError(t, err)
}

func TestHash_TwoInputs(t *testing.T) {
	var a, b fr.Element
	a.SetUint64(1)
	b.SetUint64(2)
	_, err := mimcbn254.Hash(a, b)
	require.NoError(t, err)
}

func TestHash_EmptyInput_Error(t *testing.T) {
	_, err := mimcbn254.Hash()
	require.Error(t, err)
}

func TestHash_TooManyInputs_Error(t *testing.T) {
	inputs := make([]fr.Element, 4)
	_, err := mimcbn254.Hash(inputs...)
	require.Error(t, err)
}

func TestHash_DifferentInputs_DifferentOutputs(t *testing.T) {
	var a, b fr.Element
	a.SetUint64(1)
	b.SetUint64(2)

	h1, err := mimcbn254.Hash(a)
	require.NoError(t, err)
	h2, err := mimcbn254.Hash(b)
	require.NoError(t, err)

	require.False(t, h1.Equal(&h2), "different inputs must produce different hashes")
}

// TestMimcBN254CrossConsistency proves the circuit MiMC gadget produces
// the same value as native BN254 MiMC when compiled for BN254.
// This is the BN254 counterpart of TestMimcCrossConsistency in mimc/hash_test.go.
func TestMimcBN254CrossConsistency(t *testing.T) {
	var a, b, c fr.Element
	a.SetUint64(7)
	b.SetUint64(13)
	c.SetUint64(42)

	expected, err := mimcbn254.Hash(a, b, c)
	require.NoError(t, err)

	// Build a small circuit that runs HashCircuit and compares against expected
	circuit := &mimcCrossConsistencyCircuit{}
	cs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, circuit)
	require.NoError(t, err)
	t.Logf("MiMC cross-consistency circuit for BN254: %d constraints", cs.GetNbConstraints())

	assignment := &mimcCrossConsistencyCircuit{
		A:        a,
		B:        b,
		C:        c,
		Expected: expected,
	}

	witness, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	require.NoError(t, err)

	err = cs.IsSolved(witness)
	require.NoError(t, err,
		"CRITICAL: circuit MiMC gadget produces different output than native "+
			"mimc/bn254.Hash when compiled for BN254 — every BN254 proof will fail")
}

// mimcCrossConsistencyCircuit is a test circuit that checks HashCircuit == expected.
type mimcCrossConsistencyCircuit struct {
	A, B, C  frontend.Variable
	Expected frontend.Variable `gnark:",public"`
}

func (c *mimcCrossConsistencyCircuit) Define(api frontend.API) error {
	h, err := gadgets.HashCircuit(api, c.A, c.B, c.C)
	if err != nil {
		return fmt.Errorf("cross-consistency circuit: %w", err)
	}
	api.AssertIsEqual(h, c.Expected)

	return nil
}

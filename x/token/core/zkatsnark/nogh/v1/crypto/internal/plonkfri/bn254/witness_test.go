/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	cs "github.com/consensys/gnark/constraint/bn254"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/stretchr/testify/require"
)

// TestVerifyRejectsFullWitness checks that Verify refuses a full witness
// (public and secret values) in place of the public witness, with
// errInvalidWitness, instead of treating the extra values as public inputs.
func TestVerifyRejectsFullWitness(t *testing.T) {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareCircuit{})
	require.NoError(t, err)
	spr, ok := ccs.(*cs.SparseR1CS)
	require.True(t, ok)
	pk, vk, err := Setup(spr)
	require.NoError(t, err)

	w, err := frontend.NewWitness(&squareCircuit{X: 3, Y: 9}, ecc.BN254.ScalarField())
	require.NoError(t, err)
	proof, err := Prove(spr, pk, w)
	require.NoError(t, err)

	pub, err := w.Public()
	require.NoError(t, err)
	require.NoError(t, Verify(proof, vk, pub.Vector().(fr.Vector)), "the honest proof must verify")

	full := w.Vector().(fr.Vector)
	require.Greater(t, len(full), int(vk.NbPublicVariables)) //nolint:gosec // a small test circuit
	require.ErrorIs(t, Verify(proof, vk, full), errInvalidWitness)
}

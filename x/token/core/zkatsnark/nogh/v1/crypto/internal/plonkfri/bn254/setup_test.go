/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package plonkfri_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/stretchr/testify/require"

	plonkfri "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/internal/plonkfri/bn254"
	cs "github.com/consensys/gnark/constraint/bn254"
)

// squareMulCircuit proves knowledge of X, Y such that X^2 * Y = Out.
// Same toy circuit used throughout the design discussion in PROJECT.md
// (x=3, y=2, out=18), kept here so the two stay in sync.
type squareMulCircuit struct {
	X   frontend.Variable
	Y   frontend.Variable
	Out frontend.Variable `gnark:",public"`
}

func (c *squareMulCircuit) Define(api frontend.API) error {
	t := api.Mul(c.X, c.X)
	out := api.Mul(t, c.Y)
	api.AssertIsEqual(out, c.Out)

	return nil
}

// TestSetupSmoke is a Phase 0 smoke test: it only checks that Setup runs to
// completion against today's constraint/bn254 and produces structurally
// sane keys.
func TestSetupSmoke(t *testing.T) {
	assert := require.New(t)

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareMulCircuit{})
	assert.NoError(err)

	sparseR1CS, ok := ccs.(*cs.SparseR1CS)
	assert.True(ok, "expected a *cs.SparseR1CS constraint system")

	pk, vk, err := plonkfri.Setup(sparseR1CS)
	assert.NoError(err)
	assert.NotNil(pk)
	assert.NotNil(vk)

	// ProvingKey embeds its VerifyingKey (per the struct's doc comment) --
	// Setup must return the very same object, not a copy.
	assert.Same(vk, pk.Vk)

	// one public input (Out); the small domain must be a power of two at
	// least as big as nbConstraints+nbPublic.
	assert.Equal(uint64(1), vk.NbPublicVariables)
	assert.GreaterOrEqual(vk.Size, uint64(sparseR1CS.GetNbConstraints()+1)) //nolint:gosec // a constraint count, never negative
	assert.Equal(uint64(0), vk.Size&(vk.Size-1), "domain size must be a power of two, got %d", vk.Size)
	assert.False(vk.Generator.IsZero())

	// the permutation must cover all 3*size wire slots (l||r||o).
	assert.Len(pk.Permutation, 3*int(vk.Size)) //nolint:gosec // a small test domain

	// the 8 preprocessed polynomials are committed in one Merkle tree.
	assert.NotEmpty(vk.Pre.Root, "preprocessed polynomials were not committed")
	assert.Equal(8, vk.Pre.NbPolys)
	assert.GreaterOrEqual(vk.DegreeBound, vk.Size+3, "degree bound %d too small for the blinded Z", vk.DegreeBound)
}

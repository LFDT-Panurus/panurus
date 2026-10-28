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

// TestProveSmoke is a Phase 0 smoke test: it runs Setup then Prove end-to-end
// on the same toy circuit as TestSetupSmoke, with the running example's
// witness (X=3, Y=2, Out=18), and checks the resulting Proof is
// structurally sane. Correctness is checked by TestRoundTrip.
func TestProveSmoke(t *testing.T) {
	assert := require.New(t)

	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &squareMulCircuit{})
	assert.NoError(err)
	sparseR1CS := ccs.(*cs.SparseR1CS)

	pk, _, err := plonkfri.Setup(sparseR1CS)
	assert.NoError(err)

	assignment := &squareMulCircuit{X: 3, Y: 2, Out: 18}
	fullWitness, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	assert.NoError(err)

	proof, err := plonkfri.Prove(sparseR1CS, pk, fullWitness)
	assert.NoError(err)
	assert.NotNil(proof)

	// three round commitments and one batched opening with all its queries.
	assert.NotEmpty(proof.LRO, "no commitment to l, r, o")
	assert.NotEmpty(proof.Z, "no commitment to z")
	assert.NotEmpty(proof.H, "no commitment to h1, h2, h3")
	assert.NotNil(proof.Opening)
	assert.NotEmpty(proof.Opening.Queries, "batched opening has no queries")
	for i, q := range proof.Opening.Queries {
		assert.Len(q.Commitments, 4, "query %d must open the 4 commitments", i)
	}
}

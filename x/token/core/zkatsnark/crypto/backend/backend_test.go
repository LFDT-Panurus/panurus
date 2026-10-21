/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package backend_test

import (
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/crypto/backend"
)

// trivialCircuit is a minimal gnark circuit for exercising the backend
// without pulling in the full zkatsnark circuit packages.
type trivialCircuit struct {
	X frontend.Variable
	Y frontend.Variable `gnark:",public"`
}

func (c *trivialCircuit) Define(api frontend.API) error {
	api.AssertIsEqual(c.X, c.Y)

	return nil
}

// ── Constructor ──────────────────────────────────────────────────────────────

func TestNewProofSystemBackend_Groth16(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	require.NotNil(t, b)
}

func TestNewProofSystemBackend_Plonk(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	require.NotNil(t, b)
}

func TestNewProofSystemBackend_Unsupported(t *testing.T) {
	_, err := backend.NewProofSystemBackend("spartan", ecc.BLS12_381)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported proof system")
}

func TestNewProofSystemBackend_BN254(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BN254)
	require.NoError(t, err)
	require.NotNil(t, b)

	b, err = backend.NewProofSystemBackend("plonk", ecc.BN254)
	require.NoError(t, err)
	require.NotNil(t, b)
}

// ── Groth16 round-trip ──────────────────────────────────────────────────────

func TestGroth16_SetupProveVerify_BLS12381(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, vk, err := b.Setup(cs)
	require.NoError(t, err)
	require.NotEmpty(t, pk)
	require.NotEmpty(t, vk)

	// Build a valid witness
	assignment := &trivialCircuit{X: 42, Y: 42}
	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := b.Prove(cs, pk, w)
	require.NoError(t, err)
	require.NotEmpty(t, proof)

	pubW, err := w.Public()
	require.NoError(t, err)

	err = b.Verify(vk, proof, pubW)
	require.NoError(t, err, "valid groth16 proof must verify")
}

func TestGroth16_SetupProveVerify_BN254(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BN254)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, vk, err := b.Setup(cs)
	require.NoError(t, err)

	assignment := &trivialCircuit{X: 7, Y: 7}
	w, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	require.NoError(t, err)

	proof, err := b.Prove(cs, pk, w)
	require.NoError(t, err)

	pubW, err := w.Public()
	require.NoError(t, err)

	err = b.Verify(vk, proof, pubW)
	require.NoError(t, err, "valid groth16 BN254 proof must verify")
}

func TestGroth16_VerifyRejectsWrongWitness(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, vk, err := b.Setup(cs)
	require.NoError(t, err)

	// Prove with X=42, Y=42
	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := b.Prove(cs, pk, w)
	require.NoError(t, err)

	// Verify with a different public witness (Y=99)
	wrongW, err := frontend.NewWitness(&trivialCircuit{X: 99, Y: 99}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(vk, proof, wrongW)
	require.Error(t, err, "groth16 verify must reject a proof against a wrong public witness")
}

// ── Plonk round-trip ────────────────────────────────────────────────────────

func TestPlonk_SetupProveVerify_BLS12381(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, vk, err := b.Setup(cs)
	require.NoError(t, err)
	require.NotEmpty(t, pk)
	require.NotEmpty(t, vk)

	assignment := &trivialCircuit{X: 42, Y: 42}
	w, err := frontend.NewWitness(assignment, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := b.Prove(cs, pk, w)
	require.NoError(t, err)
	require.NotEmpty(t, proof)

	pubW, err := w.Public()
	require.NoError(t, err)

	err = b.Verify(vk, proof, pubW)
	require.NoError(t, err, "valid plonk proof must verify")
}

func TestPlonk_SetupProveVerify_BN254(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BN254)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, vk, err := b.Setup(cs)
	require.NoError(t, err)

	assignment := &trivialCircuit{X: 7, Y: 7}
	w, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	require.NoError(t, err)

	proof, err := b.Prove(cs, pk, w)
	require.NoError(t, err)

	pubW, err := w.Public()
	require.NoError(t, err)

	err = b.Verify(vk, proof, pubW)
	require.NoError(t, err, "valid plonk BN254 proof must verify")
}

func TestPlonk_VerifyRejectsWrongWitness(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, vk, err := b.Setup(cs)
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	proof, err := b.Prove(cs, pk, w)
	require.NoError(t, err)

	wrongW, err := frontend.NewWitness(&trivialCircuit{X: 99, Y: 99}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(vk, proof, wrongW)
	require.Error(t, err, "plonk verify must reject a proof against a wrong public witness")
}

// ── DeserializeProvingKey / DeserializeVerifyingKey ──────────────────────────

func TestGroth16_DeserializeProvingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, _, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := b.DeserializeProvingKey(pk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

func TestGroth16_DeserializeVerifyingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vk, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := b.DeserializeVerifyingKey(vk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

func TestPlonk_DeserializeProvingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, _, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := b.DeserializeProvingKey(pk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

func TestPlonk_DeserializeVerifyingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vk, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := b.DeserializeVerifyingKey(vk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

// ── Error paths: corrupt bytes ──────────────────────────────────────────────

func TestGroth16_Verify_CorruptProof(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vk, err := b.Setup(cs)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(vk, []byte("garbage"), pubW)
	require.Error(t, err, "groth16 verify must reject garbage proof bytes")
}

func TestGroth16_Verify_CorruptVK(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify([]byte("garbage-vk"), []byte("garbage-proof"), pubW)
	require.Error(t, err, "groth16 verify must reject garbage VK bytes")
}

func TestGroth16_Prove_CorruptPK(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	_, err = b.Prove(cs, []byte("garbage-pk"), w)
	require.Error(t, err, "groth16 prove must reject garbage PK bytes")
}

func TestGroth16_DeserializeProvingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.DeserializeProvingKey([]byte("garbage"))
	require.Error(t, err)
}

func TestGroth16_DeserializeVerifyingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.DeserializeVerifyingKey([]byte("garbage"))
	require.Error(t, err)
}

func TestPlonk_Verify_CorruptProof(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vk, err := b.Setup(cs)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(vk, []byte("garbage"), pubW)
	require.Error(t, err, "plonk verify must reject garbage proof bytes")
}

func TestPlonk_Verify_CorruptVK(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify([]byte("garbage-vk"), []byte("garbage-proof"), pubW)
	require.Error(t, err, "plonk verify must reject garbage VK bytes")
}

func TestPlonk_Prove_CorruptPK(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	_, err = b.Prove(cs, []byte("garbage-pk"), w)
	require.Error(t, err, "plonk prove must reject garbage PK bytes")
}

func TestPlonk_DeserializeProvingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.DeserializeProvingKey([]byte("garbage"))
	require.Error(t, err)
}

func TestPlonk_DeserializeVerifyingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.DeserializeVerifyingKey([]byte("garbage"))
	require.Error(t, err)
}

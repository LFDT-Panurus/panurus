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

	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/backend"
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

// legacyKeyDeserializer is the shape of the deprecated
// DeserializeProvingKey/DeserializeVerifyingKey methods, which live on the
// concrete (unexported) backend types rather than on the exported
// ProofSystemBackend interface. Go interface satisfaction is structural, so
// this type assertion works against the unexported dynamic type returned by
// NewProofSystemBackend without this external test package needing to name
// it.
type legacyKeyDeserializer interface {
	DeserializeProvingKey(raw []byte) (any, error)
	DeserializeVerifyingKey(raw []byte) (any, error)
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

	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)
	require.NotEmpty(t, pkBytes)
	require.NotEmpty(t, vkBytes)

	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
	require.NoError(t, err)

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

	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)

	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
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

	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)

	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
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

	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)
	require.NotEmpty(t, pkBytes)
	require.NotEmpty(t, vkBytes)

	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
	require.NoError(t, err)

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

	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)

	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
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

	pkBytes, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)

	pk, err := b.NewProvingKey(pkBytes)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
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

// ── Cross-system / cross-curve key safety ────────────────────────────────────
//
// These are the tests that prove the gnark panic hazard identified in the
// design is closed: handing a key to a backend for the wrong proof system or
// the wrong curve must return an error, never reach gnark's internal type
// assertion (which panics on mismatch).

func TestGroth16_Prove_RejectsPlonkKey(t *testing.T) {
	g, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	p, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)
	plonkCS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	plonkPKBytes, _, err := p.Setup(plonkCS)
	require.NoError(t, err)
	plonkPK, err := p.NewProvingKey(plonkPKBytes)
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	_, err = g.Prove(cs, plonkPK, w)
	require.Error(t, err, "groth16 backend must reject a plonk proving key rather than panic")
	assert.Contains(t, err.Error(), "plonk")
}

func TestGroth16_Verify_RejectsPlonkKey(t *testing.T) {
	g, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	p, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	plonkCS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)
	_, plonkVKBytes, err := p.Setup(plonkCS)
	require.NoError(t, err)
	plonkVK, err := p.NewVerifyingKey(plonkVKBytes)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = g.Verify(plonkVK, []byte("irrelevant-proof-bytes"), pubW)
	require.Error(t, err, "groth16 backend must reject a plonk verifying key rather than panic")
	assert.Contains(t, err.Error(), "plonk")
}

func TestGroth16_Prove_RejectsWrongCurveKey(t *testing.T) {
	bls, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	bn, err := backend.NewProofSystemBackend("groth16", ecc.BN254)
	require.NoError(t, err)

	blsCS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	bnCS, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)
	bnPKBytes, _, err := bn.Setup(bnCS)
	require.NoError(t, err)
	bnPK, err := bn.NewProvingKey(bnPKBytes)
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	_, err = bls.Prove(blsCS, bnPK, w)
	require.Error(t, err, "groth16 backend must reject a key deserialized for a different curve rather than panic")
}

func TestGroth16_Prove_RejectsNilKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	_, err = b.Prove(cs, nil, w)
	require.Error(t, err, "groth16 backend must reject a nil proving key")
}

func TestGroth16_Verify_RejectsNilKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(nil, []byte("irrelevant"), pubW)
	require.Error(t, err, "groth16 backend must reject a nil verifying key")
}

func TestPlonk_Prove_RejectsGroth16Key(t *testing.T) {
	p, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	g, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	plonkCS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)
	groth16CS, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	groth16PKBytes, _, err := g.Setup(groth16CS)
	require.NoError(t, err)
	groth16PK, err := g.NewProvingKey(groth16PKBytes)
	require.NoError(t, err)

	w, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField())
	require.NoError(t, err)

	_, err = p.Prove(plonkCS, groth16PK, w)
	require.Error(t, err, "plonk backend must reject a groth16 proving key rather than panic")
	assert.Contains(t, err.Error(), "groth16")
}

func TestPlonk_Verify_RejectsWrongCurveKey(t *testing.T) {
	bls, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	bn, err := backend.NewProofSystemBackend("plonk", ecc.BN254)
	require.NoError(t, err)

	bnCS, err := frontend.Compile(ecc.BN254.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)
	_, bnVKBytes, err := bn.Setup(bnCS)
	require.NoError(t, err)
	bnVK, err := bn.NewVerifyingKey(bnVKBytes)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = bls.Verify(bnVK, []byte("irrelevant-proof-bytes"), pubW)
	require.Error(t, err, "plonk backend must reject a key deserialized for a different curve rather than panic")
}

// ── DeserializeProvingKey / DeserializeVerifyingKey (deprecated) ────────────

func TestGroth16_DeserializeProvingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, _, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := d.DeserializeProvingKey(pk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

func TestGroth16_DeserializeVerifyingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vk, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := d.DeserializeVerifyingKey(vk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

func TestPlonk_DeserializeProvingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	pk, _, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := d.DeserializeProvingKey(pk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

func TestPlonk_DeserializeVerifyingKey(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vk, err := b.Setup(cs)
	require.NoError(t, err)

	deserialized, err := d.DeserializeVerifyingKey(vk)
	require.NoError(t, err)
	require.NotNil(t, deserialized)
}

// ── Error paths: corrupt bytes ──────────────────────────────────────────────

func TestGroth16_Verify_CorruptProof(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), r1cs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(vk, []byte("garbage"), pubW)
	require.Error(t, err, "groth16 verify must reject garbage proof bytes")
}

func TestGroth16_NewVerifyingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.NewVerifyingKey([]byte("garbage-vk"))
	require.Error(t, err, "groth16 backend must reject garbage VK bytes")
}

func TestGroth16_NewProvingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.NewProvingKey([]byte("garbage-pk"))
	require.Error(t, err, "groth16 backend must reject garbage PK bytes")
}

func TestGroth16_DeserializeProvingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	_, err = d.DeserializeProvingKey([]byte("garbage"))
	require.Error(t, err)
}

func TestGroth16_DeserializeVerifyingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("groth16", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	_, err = d.DeserializeVerifyingKey([]byte("garbage"))
	require.Error(t, err)
}

func TestPlonk_Verify_CorruptProof(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	cs, err := frontend.Compile(ecc.BLS12_381.ScalarField(), scs.NewBuilder, &trivialCircuit{})
	require.NoError(t, err)

	_, vkBytes, err := b.Setup(cs)
	require.NoError(t, err)
	vk, err := b.NewVerifyingKey(vkBytes)
	require.NoError(t, err)

	pubW, err := frontend.NewWitness(&trivialCircuit{X: 42, Y: 42}, ecc.BLS12_381.ScalarField(), frontend.PublicOnly())
	require.NoError(t, err)

	err = b.Verify(vk, []byte("garbage"), pubW)
	require.Error(t, err, "plonk verify must reject garbage proof bytes")
}

func TestPlonk_NewVerifyingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.NewVerifyingKey([]byte("garbage-vk"))
	require.Error(t, err, "plonk backend must reject garbage VK bytes")
}

func TestPlonk_NewProvingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)

	_, err = b.NewProvingKey([]byte("garbage-pk"))
	require.Error(t, err, "plonk backend must reject garbage PK bytes")
}

func TestPlonk_DeserializeProvingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	_, err = d.DeserializeProvingKey([]byte("garbage"))
	require.Error(t, err)
}

func TestPlonk_DeserializeVerifyingKey_CorruptBytes(t *testing.T) {
	b, err := backend.NewProofSystemBackend("plonk", ecc.BLS12_381)
	require.NoError(t, err)
	d, ok := b.(legacyKeyDeserializer)
	require.True(t, ok)

	_, err = d.DeserializeVerifyingKey([]byte("garbage"))
	require.Error(t, err)
}

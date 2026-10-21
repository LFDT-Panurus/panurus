/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package backend provides the ProofSystemBackend interface and concrete
// implementations for Groth16 and Plonk. This is the single abstraction
// layer that setup, prover, and validator all route through instead of
// calling groth16.* or plonk.* directly (Decision A).
//
// The interface speaks bytes in and bytes out, which is a natural fit since
// the wire format (SpendDescription.SpendProof []byte, etc.) already
// doesn't care what produced those bytes.
package backend

import (
	"bytes"
	"io"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/kzg"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/test/unsafekzg"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// ProofSystemBackend abstracts over different ZK proof systems (Groth16,
// Plonk) at the byte level. groth16.Proof and plonk.Proof are different
// concrete types with no shared gnark-exported interface, so this wraps
// Setup/Prove/Verify behind an interface that speaks bytes.
type ProofSystemBackend interface {
	// Setup runs the trusted setup ceremony for the given constraint system.
	// Returns serialized proving key and verification key bytes.
	Setup(cs constraint.ConstraintSystem) (pk, vk []byte, err error)

	// Prove generates a proof for the given constraint system, proving key
	// bytes, and witness.
	Prove(cs constraint.ConstraintSystem, pk []byte, w witness.Witness) ([]byte, error)

	// Verify checks a proof against the verification key and public witness.
	Verify(vk []byte, proof []byte, publicWitness witness.Witness) error

	// DeserializeProvingKey reconstructs a proving key from bytes.
	DeserializeProvingKey(raw []byte) (any, error)

	// DeserializeVerifyingKey reconstructs a verifying key from bytes.
	DeserializeVerifyingKey(raw []byte) (any, error)
}

// ── Groth16 backend ────────────────────────────────────────────────────────────

type groth16Backend struct {
	curve ecc.ID
}

// Setup implements ProofSystemBackend.Setup for Groth16.
func (b *groth16Backend) Setup(cs constraint.ConstraintSystem) ([]byte, []byte, error) {
	pk, vk, err := groth16.Setup(cs)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "groth16 backend: setup failed")
	}

	pkBytes, err := serializeWriterTo(pk)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "groth16 backend: proving key serialization")
	}

	vkBytes, err := serializeWriterTo(vk)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "groth16 backend: verifying key serialization")
	}

	return pkBytes, vkBytes, nil
}

// Prove implements ProofSystemBackend.Prove for Groth16.
func (b *groth16Backend) Prove(cs constraint.ConstraintSystem, pkBytes []byte, w witness.Witness) ([]byte, error) {
	pk := groth16.NewProvingKey(b.curve)
	if _, err := pk.ReadFrom(bytes.NewReader(pkBytes)); err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: proving key deserialization")
	}

	proof, err := groth16.Prove(cs, pk, w)
	if err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: prove failed")
	}

	return serializeWriterTo(proof)
}

// Verify implements ProofSystemBackend.Verify for Groth16.
func (b *groth16Backend) Verify(vkBytes []byte, proofBytes []byte, publicWitness witness.Witness) error {
	vk := groth16.NewVerifyingKey(b.curve)
	if _, err := vk.ReadFrom(bytes.NewReader(vkBytes)); err != nil {
		return errors.Wrapf(err, "groth16 backend: verifying key deserialization")
	}

	proof := groth16.NewProof(b.curve)
	if _, err := proof.ReadFrom(bytes.NewReader(proofBytes)); err != nil {
		return errors.Wrapf(err, "groth16 backend: proof deserialization")
	}

	return groth16.Verify(proof, vk, publicWitness)
}

// DeserializeProvingKey implements ProofSystemBackend.DeserializeProvingKey for Groth16.
func (b *groth16Backend) DeserializeProvingKey(raw []byte) (any, error) {
	pk := groth16.NewProvingKey(b.curve)
	if _, err := pk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: proving key deserialization")
	}

	return pk, nil
}

// DeserializeVerifyingKey implements ProofSystemBackend.DeserializeVerifyingKey for Groth16.
func (b *groth16Backend) DeserializeVerifyingKey(raw []byte) (any, error) {
	vk := groth16.NewVerifyingKey(b.curve)
	if _, err := vk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: verifying key deserialization")
	}

	return vk, nil
}

// ── Plonk backend ──────────────────────────────────────────────────────────────

type plonkBackend struct {
	curve ecc.ID
	// srs and srsLagrange are the universal KZG structured reference string,
	// sized once at construction. Not passed per-call since it's universal
	// across circuits.
	srs         kzg.SRS
	srsLagrange kzg.SRS
}

// Setup implements ProofSystemBackend.Setup for Plonk.
// Uses the pre-constructed KZG SRS stored in the backend.
func (b *plonkBackend) Setup(cs constraint.ConstraintSystem) ([]byte, []byte, error) {
	// If no SRS was provided at construction, generate one from the
	// constraint system (test-only path using unsafekzg).
	srs, srsLag := b.srs, b.srsLagrange
	if srs == nil || srsLag == nil {
		var err error
		srs, srsLag, err = unsafekzg.NewSRS(cs)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "plonk backend: SRS generation failed")
		}
		// Cache for subsequent Setup calls on different circuits.
		// This is safe for test usage where all circuits share the same SRS.
		b.srs = srs
		b.srsLagrange = srsLag
	}

	pk, vk, err := plonk.Setup(cs, srs, srsLag)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "plonk backend: setup failed")
	}

	pkBytes, err := serializeWriterTo(pk)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "plonk backend: proving key serialization")
	}

	vkBytes, err := serializeWriterTo(vk)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "plonk backend: verifying key serialization")
	}

	return pkBytes, vkBytes, nil
}

// Prove implements ProofSystemBackend.Prove for Plonk.
func (b *plonkBackend) Prove(cs constraint.ConstraintSystem, pkBytes []byte, w witness.Witness) ([]byte, error) {
	pk := plonk.NewProvingKey(b.curve)
	if _, err := pk.ReadFrom(bytes.NewReader(pkBytes)); err != nil {
		return nil, errors.Wrapf(err, "plonk backend: proving key deserialization")
	}

	proof, err := plonk.Prove(cs, pk, w)
	if err != nil {
		return nil, errors.Wrapf(err, "plonk backend: prove failed")
	}

	return serializeWriterTo(proof)
}

// Verify implements ProofSystemBackend.Verify for Plonk.
func (b *plonkBackend) Verify(vkBytes []byte, proofBytes []byte, publicWitness witness.Witness) error {
	vk := plonk.NewVerifyingKey(b.curve)
	if _, err := vk.ReadFrom(bytes.NewReader(vkBytes)); err != nil {
		return errors.Wrapf(err, "plonk backend: verifying key deserialization")
	}

	proof := plonk.NewProof(b.curve)
	if _, err := proof.ReadFrom(bytes.NewReader(proofBytes)); err != nil {
		return errors.Wrapf(err, "plonk backend: proof deserialization")
	}

	return plonk.Verify(proof, vk, publicWitness)
}

// DeserializeProvingKey implements ProofSystemBackend.DeserializeProvingKey for Plonk.
func (b *plonkBackend) DeserializeProvingKey(raw []byte) (any, error) {
	pk := plonk.NewProvingKey(b.curve)
	if _, err := pk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "plonk backend: proving key deserialization")
	}

	return pk, nil
}

// DeserializeVerifyingKey implements ProofSystemBackend.DeserializeVerifyingKey for Plonk.
func (b *plonkBackend) DeserializeVerifyingKey(raw []byte) (any, error) {
	vk := plonk.NewVerifyingKey(b.curve)
	if _, err := vk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "plonk backend: verifying key deserialization")
	}

	return vk, nil
}

// ── Constructor ────────────────────────────────────────────────────────────────

// NewProofSystemBackend creates the appropriate backend for the given proof
// system name and curve. Dispatches on proofSystem: "groth16" → groth16Backend,
// "plonk" → plonkBackend.
//
//nolint:ireturn // returns ProofSystemBackend interface to select between groth16 and plonk implementations
func NewProofSystemBackend(proofSystem string, curve ecc.ID) (ProofSystemBackend, error) {
	switch proofSystem {
	case "groth16":
		return &groth16Backend{curve: curve}, nil
	case "plonk":
		return &plonkBackend{curve: curve}, nil
	default:
		return nil, errors.Errorf("backend: unsupported proof system %q", proofSystem)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

type writerTo interface {
	WriteTo(w io.Writer) (int64, error)
}

func serializeWriterTo(obj writerTo) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := obj.WriteTo(&buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

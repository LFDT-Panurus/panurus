/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

// Package backend provides the ProofSystemBackend interface and concrete
// implementations for Groth16 and Plonk. This is the single abstraction
// layer that setup, prover, and validator all route through instead of
// calling groth16.* or plonk.* directly (Decision A).
//
// Setup speaks bytes, since that is the wire format distributed in
// PublicParams. Prove and Verify speak ready-to-use key handles
// (ProvingKey/VerifyingKey): deserializing a proving or verifying key is not
// cheap, and a backend used on a hot path (one Prove/Verify call per proof)
// must not pay that cost more than once per key. Callers deserialize a key
// once, via NewProvingKey/NewVerifyingKey, and reuse the resulting handle
// across every subsequent Prove/Verify call — see package prepared, which
// builds these handles when public parameters are installed.
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

const (
	systemGroth16 = "groth16"
	systemPlonk   = "plonk"
)

// ProvingKey is an opaque, already-deserialized proving key produced by
// ProofSystemBackend.NewProvingKey. It is immutable after construction and
// safe for concurrent use by any number of goroutines (gnark's Prove does
// not mutate the underlying key). A ProvingKey is only ever valid for the
// proof system and curve it was deserialized for; passing it to a backend
// for a different system or curve returns an error rather than panicking.
type ProvingKey struct {
	system string
	curve  ecc.ID
	key    any // groth16.ProvingKey or plonk.ProvingKey
}

// VerifyingKey is the verifying-key counterpart of ProvingKey. Same
// immutability, concurrency, and system/curve-provenance guarantees.
type VerifyingKey struct {
	system string
	curve  ecc.ID
	key    any // groth16.VerifyingKey or plonk.VerifyingKey
}

// ProofSystemBackend abstracts over different ZK proof systems (Groth16,
// Plonk). groth16.Proof and plonk.Proof are different concrete types with no
// shared gnark-exported interface, so this wraps Setup/Prove/Verify behind a
// single interface selectable at runtime by proof system name.
type ProofSystemBackend interface {
	// Setup runs the trusted setup ceremony for the given constraint system.
	// Returns serialized proving key and verification key bytes, suitable
	// for storing in PublicParams for distribution. Call once, offline.
	Setup(cs constraint.ConstraintSystem) (pk, vk []byte, err error)

	// NewProvingKey deserializes raw into a ready-to-use ProvingKey for this
	// backend's proof system and curve. Call once per key; the result is
	// reusable and concurrency-safe across every subsequent Prove call.
	NewProvingKey(raw []byte) (*ProvingKey, error)

	// NewVerifyingKey deserializes raw into a ready-to-use VerifyingKey.
	// Call once per key; the result is reusable and concurrency-safe across
	// every subsequent Verify call.
	NewVerifyingKey(raw []byte) (*VerifyingKey, error)

	// Prove generates a proof for the given constraint system, already
	// deserialized proving key, and witness. pk must have been produced by
	// this backend's NewProvingKey for a matching proof system and curve.
	Prove(cs constraint.ConstraintSystem, pk *ProvingKey, w witness.Witness) ([]byte, error)

	// Verify checks a proof against an already deserialized verifying key
	// and public witness. vk must have been produced by this backend's
	// NewVerifyingKey for a matching proof system and curve.
	Verify(vk *VerifyingKey, proof []byte, publicWitness witness.Witness) error
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

// NewProvingKey implements ProofSystemBackend.NewProvingKey for Groth16.
func (b *groth16Backend) NewProvingKey(raw []byte) (*ProvingKey, error) {
	pk, err := b.deserializeProvingKey(raw)
	if err != nil {
		return nil, err
	}

	return &ProvingKey{system: systemGroth16, curve: b.curve, key: pk}, nil
}

// NewVerifyingKey implements ProofSystemBackend.NewVerifyingKey for Groth16.
func (b *groth16Backend) NewVerifyingKey(raw []byte) (*VerifyingKey, error) {
	vk, err := b.deserializeVerifyingKey(raw)
	if err != nil {
		return nil, err
	}

	return &VerifyingKey{system: systemGroth16, curve: b.curve, key: vk}, nil
}

// Prove implements ProofSystemBackend.Prove for Groth16.
func (b *groth16Backend) Prove(cs constraint.ConstraintSystem, pk *ProvingKey, w witness.Witness) ([]byte, error) {
	key, err := b.provingKey(pk)
	if err != nil {
		return nil, err
	}

	proof, err := groth16.Prove(cs, key, w)
	if err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: prove failed")
	}

	return serializeWriterTo(proof)
}

// Verify implements ProofSystemBackend.Verify for Groth16.
func (b *groth16Backend) Verify(vk *VerifyingKey, proofBytes []byte, publicWitness witness.Witness) error {
	key, err := b.verifyingKey(vk)
	if err != nil {
		return err
	}

	proof := groth16.NewProof(b.curve)
	if _, err := proof.ReadFrom(bytes.NewReader(proofBytes)); err != nil {
		return errors.Wrapf(err, "groth16 backend: proof deserialization")
	}

	return groth16.Verify(proof, key, publicWitness)
}

// DeserializeProvingKey reconstructs a Groth16 proving key from bytes,
// returning the raw gnark type rather than the opaque ProvingKey handle.
//
// Deprecated: use NewProvingKey, which returns a typed, system/curve-checked
// handle usable directly with Prove.
func (b *groth16Backend) DeserializeProvingKey(raw []byte) (any, error) {
	return b.deserializeProvingKey(raw)
}

// DeserializeVerifyingKey reconstructs a Groth16 verifying key from bytes,
// returning the raw gnark type rather than the opaque VerifyingKey handle.
//
// Deprecated: use NewVerifyingKey, which returns a typed, system/curve-checked
// handle usable directly with Verify.
func (b *groth16Backend) DeserializeVerifyingKey(raw []byte) (any, error) {
	return b.deserializeVerifyingKey(raw)
}

func (b *groth16Backend) deserializeProvingKey(raw []byte) (groth16.ProvingKey, error) {
	pk := groth16.NewProvingKey(b.curve)
	if _, err := pk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: proving key deserialization")
	}

	return pk, nil
}

func (b *groth16Backend) deserializeVerifyingKey(raw []byte) (groth16.VerifyingKey, error) {
	vk := groth16.NewVerifyingKey(b.curve)
	if _, err := vk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "groth16 backend: verifying key deserialization")
	}

	return vk, nil
}

// provingKey validates that pk was produced for Groth16 and this backend's
// curve, and returns the underlying gnark key. This is what turns a
// system/curve mismatch into a returned error instead of a panic inside
// groth16.Prove's internal type assertion.
func (b *groth16Backend) provingKey(pk *ProvingKey) (groth16.ProvingKey, error) {
	if pk == nil {
		return nil, errors.New("groth16 backend: proving key is nil")
	}
	if pk.system != systemGroth16 {
		return nil, errors.Errorf("groth16 backend: proving key is for proof system %q, not %q", pk.system, systemGroth16)
	}
	if pk.curve != b.curve {
		return nil, errors.Errorf("groth16 backend: proving key is for curve %s, backend is for curve %s", pk.curve, b.curve)
	}
	key, ok := pk.key.(groth16.ProvingKey)
	if !ok {
		return nil, errors.Errorf("groth16 backend: proving key holds unexpected type %T", pk.key)
	}

	return key, nil
}

// verifyingKey is the VerifyingKey counterpart of provingKey.
func (b *groth16Backend) verifyingKey(vk *VerifyingKey) (groth16.VerifyingKey, error) {
	if vk == nil {
		return nil, errors.New("groth16 backend: verifying key is nil")
	}
	if vk.system != systemGroth16 {
		return nil, errors.Errorf("groth16 backend: verifying key is for proof system %q, not %q", vk.system, systemGroth16)
	}
	if vk.curve != b.curve {
		return nil, errors.Errorf("groth16 backend: verifying key is for curve %s, backend is for curve %s", vk.curve, b.curve)
	}
	key, ok := vk.key.(groth16.VerifyingKey)
	if !ok {
		return nil, errors.Errorf("groth16 backend: verifying key holds unexpected type %T", vk.key)
	}

	return key, nil
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

// NewProvingKey implements ProofSystemBackend.NewProvingKey for Plonk.
func (b *plonkBackend) NewProvingKey(raw []byte) (*ProvingKey, error) {
	pk, err := b.deserializeProvingKey(raw)
	if err != nil {
		return nil, err
	}

	return &ProvingKey{system: systemPlonk, curve: b.curve, key: pk}, nil
}

// NewVerifyingKey implements ProofSystemBackend.NewVerifyingKey for Plonk.
func (b *plonkBackend) NewVerifyingKey(raw []byte) (*VerifyingKey, error) {
	vk, err := b.deserializeVerifyingKey(raw)
	if err != nil {
		return nil, err
	}

	return &VerifyingKey{system: systemPlonk, curve: b.curve, key: vk}, nil
}

// Prove implements ProofSystemBackend.Prove for Plonk.
func (b *plonkBackend) Prove(cs constraint.ConstraintSystem, pk *ProvingKey, w witness.Witness) ([]byte, error) {
	key, err := b.provingKey(pk)
	if err != nil {
		return nil, err
	}

	proof, err := plonk.Prove(cs, key, w)
	if err != nil {
		return nil, errors.Wrapf(err, "plonk backend: prove failed")
	}

	return serializeWriterTo(proof)
}

// Verify implements ProofSystemBackend.Verify for Plonk.
func (b *plonkBackend) Verify(vk *VerifyingKey, proofBytes []byte, publicWitness witness.Witness) error {
	key, err := b.verifyingKey(vk)
	if err != nil {
		return err
	}

	proof := plonk.NewProof(b.curve)
	if _, err := proof.ReadFrom(bytes.NewReader(proofBytes)); err != nil {
		return errors.Wrapf(err, "plonk backend: proof deserialization")
	}

	return plonk.Verify(proof, key, publicWitness)
}

// DeserializeProvingKey reconstructs a Plonk proving key from bytes,
// returning the raw gnark type rather than the opaque ProvingKey handle.
//
// Deprecated: use NewProvingKey, which returns a typed, system/curve-checked
// handle usable directly with Prove.
func (b *plonkBackend) DeserializeProvingKey(raw []byte) (any, error) {
	return b.deserializeProvingKey(raw)
}

// DeserializeVerifyingKey reconstructs a Plonk verifying key from bytes,
// returning the raw gnark type rather than the opaque VerifyingKey handle.
//
// Deprecated: use NewVerifyingKey, which returns a typed, system/curve-checked
// handle usable directly with Verify.
func (b *plonkBackend) DeserializeVerifyingKey(raw []byte) (any, error) {
	return b.deserializeVerifyingKey(raw)
}

func (b *plonkBackend) deserializeProvingKey(raw []byte) (plonk.ProvingKey, error) {
	pk := plonk.NewProvingKey(b.curve)
	if _, err := pk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "plonk backend: proving key deserialization")
	}

	return pk, nil
}

func (b *plonkBackend) deserializeVerifyingKey(raw []byte) (plonk.VerifyingKey, error) {
	vk := plonk.NewVerifyingKey(b.curve)
	if _, err := vk.ReadFrom(bytes.NewReader(raw)); err != nil {
		return nil, errors.Wrapf(err, "plonk backend: verifying key deserialization")
	}

	return vk, nil
}

// provingKey validates that pk was produced for Plonk and this backend's
// curve, and returns the underlying gnark key. This is what turns a
// system/curve mismatch into a returned error instead of a panic inside
// plonk.Prove's internal type assertion.
func (b *plonkBackend) provingKey(pk *ProvingKey) (plonk.ProvingKey, error) {
	if pk == nil {
		return nil, errors.New("plonk backend: proving key is nil")
	}
	if pk.system != systemPlonk {
		return nil, errors.Errorf("plonk backend: proving key is for proof system %q, not %q", pk.system, systemPlonk)
	}
	if pk.curve != b.curve {
		return nil, errors.Errorf("plonk backend: proving key is for curve %s, backend is for curve %s", pk.curve, b.curve)
	}
	key, ok := pk.key.(plonk.ProvingKey)
	if !ok {
		return nil, errors.Errorf("plonk backend: proving key holds unexpected type %T", pk.key)
	}

	return key, nil
}

// verifyingKey is the VerifyingKey counterpart of provingKey.
func (b *plonkBackend) verifyingKey(vk *VerifyingKey) (plonk.VerifyingKey, error) {
	if vk == nil {
		return nil, errors.New("plonk backend: verifying key is nil")
	}
	if vk.system != systemPlonk {
		return nil, errors.Errorf("plonk backend: verifying key is for proof system %q, not %q", vk.system, systemPlonk)
	}
	if vk.curve != b.curve {
		return nil, errors.Errorf("plonk backend: verifying key is for curve %s, backend is for curve %s", vk.curve, b.curve)
	}
	key, ok := vk.key.(plonk.VerifyingKey)
	if !ok {
		return nil, errors.Errorf("plonk backend: verifying key holds unexpected type %T", vk.key)
	}

	return key, nil
}

// ── Constructor ────────────────────────────────────────────────────────────────

// NewProofSystemBackend creates the appropriate backend for the given proof
// system name and curve. Dispatches on proofSystem: "groth16" → groth16Backend,
// "plonk" → plonkBackend.
//
//nolint:ireturn // returns ProofSystemBackend interface to select between groth16 and plonk implementations
func NewProofSystemBackend(proofSystem string, curve ecc.ID) (ProofSystemBackend, error) {
	switch proofSystem {
	case systemGroth16:
		return &groth16Backend{curve: curve}, nil
	case systemPlonk:
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

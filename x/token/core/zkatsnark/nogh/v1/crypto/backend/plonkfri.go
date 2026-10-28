/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package backend

import (
	"github.com/consensys/gnark-crypto/ecc"
	fr_bn254 "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/backend/witness"
	"github.com/consensys/gnark/constraint"
	cs_bn254 "github.com/consensys/gnark/constraint/bn254"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	plonkfri "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/crypto/internal/plonkfri/bn254"
)

const systemPlonkFRI = "plonkfri"

// plonkFRIBackend implements ProofSystemBackend for PLONK with FRI as the
// polynomial commitment scheme. Unlike plonk it needs no SRS: the setup is
// transparent and deterministic. Only bn254 is supported by the vendored
// implementation (package crypto/internal/plonkfri/bn254), and the
// constraint system must be a SparseR1CS (scs builder).
type plonkFRIBackend struct {
	curve ecc.ID
}

func newPlonkFRIBackend(curve ecc.ID) (*plonkFRIBackend, error) {
	if curve != ecc.BN254 {
		return nil, errors.Errorf("backend: proof system %q supports only curve %s, got %s", systemPlonkFRI, ecc.BN254, curve)
	}

	return &plonkFRIBackend{curve: curve}, nil
}

// sparseR1CS returns cs as the bn254 SparseR1CS plonkfri requires.
func (b *plonkFRIBackend) sparseR1CS(cs constraint.ConstraintSystem) (*cs_bn254.SparseR1CS, error) {
	// In gnark, cs_bn254.R1CS and cs_bn254.SparseR1CS are aliases of the same
	// Go type, so the type assertion alone cannot tell them apart: the system
	// kind must be checked too, or an R1CS would be fed to plonkfri.Setup.
	spr, ok := cs.(*cs_bn254.SparseR1CS)
	if !ok || spr.Type != constraint.SystemSparseR1CS {
		return nil, errors.Errorf("plonkfri backend: constraint system must be a bn254 SparseR1CS (scs builder), got %T", cs)
	}

	return spr, nil
}

// Setup implements ProofSystemBackend.Setup for PLONK-FRI. It is
// deterministic and needs no trusted setup material.
func (b *plonkFRIBackend) Setup(cs constraint.ConstraintSystem) ([]byte, []byte, error) {
	spr, err := b.sparseR1CS(cs)
	if err != nil {
		return nil, nil, err
	}

	pk, vk, err := plonkfri.Setup(spr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "plonkfri backend: setup failed")
	}

	pkBytes, err := serializeWriterTo(pk)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "plonkfri backend: proving key serialization")
	}

	vkBytes, err := serializeWriterTo(vk)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "plonkfri backend: verifying key serialization")
	}

	return pkBytes, vkBytes, nil
}

// NewProvingKey implements ProofSystemBackend.NewProvingKey for PLONK-FRI.
// Decoding recomputes the derived key material and checks it against the
// embedded verifying key, so it is considerably more expensive than for the
// other systems: call it once per key.
func (b *plonkFRIBackend) NewProvingKey(raw []byte) (*ProvingKey, error) {
	pk := &plonkfri.ProvingKey{}
	if err := readFromExact(pk, raw); err != nil {
		return nil, errors.Wrapf(err, "plonkfri backend: proving key deserialization")
	}

	return &ProvingKey{system: systemPlonkFRI, curve: b.curve, key: pk}, nil
}

// NewVerifyingKey implements ProofSystemBackend.NewVerifyingKey for PLONK-FRI.
func (b *plonkFRIBackend) NewVerifyingKey(raw []byte) (*VerifyingKey, error) {
	vk := &plonkfri.VerifyingKey{}
	if err := readFromExact(vk, raw); err != nil {
		return nil, errors.Wrapf(err, "plonkfri backend: verifying key deserialization")
	}

	return &VerifyingKey{system: systemPlonkFRI, curve: b.curve, key: vk}, nil
}

// Prove implements ProofSystemBackend.Prove for PLONK-FRI.
func (b *plonkFRIBackend) Prove(cs constraint.ConstraintSystem, pk *ProvingKey, w witness.Witness) ([]byte, error) {
	spr, err := b.sparseR1CS(cs)
	if err != nil {
		return nil, err
	}
	key, err := b.provingKey(pk)
	if err != nil {
		return nil, err
	}

	proof, err := plonkfri.Prove(spr, key, w)
	if err != nil {
		return nil, errors.Wrapf(err, "plonkfri backend: prove failed")
	}

	return serializeWriterTo(proof)
}

// Verify implements ProofSystemBackend.Verify for PLONK-FRI.
func (b *plonkFRIBackend) Verify(vk *VerifyingKey, proofBytes []byte, publicWitness witness.Witness) error {
	key, err := b.verifyingKey(vk)
	if err != nil {
		return err
	}

	if publicWitness == nil {
		return errors.New("plonkfri backend: public witness is nil")
	}
	public, ok := publicWitness.Vector().(fr_bn254.Vector)
	if !ok {
		return errors.Wrapf(witness.ErrInvalidWitness, "plonkfri backend: public witness is not a bn254 vector, got %T", publicWitness.Vector())
	}

	proof := &plonkfri.Proof{}
	if err := readFromExact(proof, proofBytes); err != nil {
		return errors.Wrapf(err, "plonkfri backend: proof deserialization")
	}

	return plonkfri.Verify(proof, key, public)
}

// provingKey validates that pk was produced for PLONK-FRI and this backend's
// curve, and returns the underlying key.
func (b *plonkFRIBackend) provingKey(pk *ProvingKey) (*plonkfri.ProvingKey, error) {
	if pk == nil {
		return nil, errors.New("plonkfri backend: proving key is nil")
	}
	if pk.system != systemPlonkFRI {
		return nil, errors.Errorf("plonkfri backend: proving key is for proof system %q, not %q", pk.system, systemPlonkFRI)
	}
	if pk.curve != b.curve {
		return nil, errors.Errorf("plonkfri backend: proving key is for curve %s, backend is for curve %s", pk.curve, b.curve)
	}
	key, ok := pk.key.(*plonkfri.ProvingKey)
	if !ok {
		return nil, errors.Errorf("plonkfri backend: proving key holds unexpected type %T", pk.key)
	}

	return key, nil
}

// verifyingKey is the VerifyingKey counterpart of provingKey.
func (b *plonkFRIBackend) verifyingKey(vk *VerifyingKey) (*plonkfri.VerifyingKey, error) {
	if vk == nil {
		return nil, errors.New("plonkfri backend: verifying key is nil")
	}
	if vk.system != systemPlonkFRI {
		return nil, errors.Errorf("plonkfri backend: verifying key is for proof system %q, not %q", vk.system, systemPlonkFRI)
	}
	if vk.curve != b.curve {
		return nil, errors.Errorf("plonkfri backend: verifying key is for curve %s, backend is for curve %s", vk.curve, b.curve)
	}
	key, ok := vk.key.(*plonkfri.VerifyingKey)
	if !ok {
		return nil, errors.Errorf("plonkfri backend: verifying key holds unexpected type %T", vk.key)
	}

	return key, nil
}

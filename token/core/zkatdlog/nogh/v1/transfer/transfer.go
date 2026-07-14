/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type Prover interface {
	Prove() ([]byte, error)
	RangeProofType() rp.ProofType
}

// NewProver returns a new Prover instance.
func NewProver(inputWitness, outputWitness []*token.Metadata, inputs, outputs []*math.G1, pp *v1.PublicParams) (Prover, error) {
	if pp.CSPRangeProofParams != nil {
		return NewCSPBasedProver(inputWitness, outputWitness, inputs, outputs, pp)
	}

	return NewBulletProofProver(inputWitness, outputWitness, inputs, outputs, pp)
}

type Verifier interface {
	Verify(proofRaw []byte) error
}

// NewVerifier returns a Verifier for the given proofType.
// It returns ErrProofTypeMismatch if the params sub-struct required by proofType
// is not populated in pp, preventing an attacker from selecting a verifier whose
// params sub-struct is nil. Both proof systems may coexist in pp (e.g. during a
// range-proof migration), so each is checked independently.
func NewVerifier(inputs, outputs []*math.G1, pp *v1.PublicParams, proofType rp.ProofType) (Verifier, error) {
	if !pp.SupportsRangeProofType(proofType) {
		return nil, errors.Errorf("%w: proof type %d is not available in public parameters",
			ErrProofTypeMismatch, proofType)
	}

	if proofType == rp.RangeProofType {
		return NewBulletProofVerifier(inputs, outputs, pp), nil
	}

	return NewCSPVerifier(inputs, outputs, pp), nil
}

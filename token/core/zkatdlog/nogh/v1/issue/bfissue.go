/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/bulletproof"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// BulletProof proves that an IssueAction is valid by demonstrating that all issued tokens
// have the same type and that their values are within the authorized range.
type BulletProof struct {
	// SameType is the proof that all issued tokens have the same type.
	SameType *SameType
	// RangeCorrectness is the proof that issued tokens have values in the authorized range.
	RangeCorrectness *bulletproof.RangeCorrectness
}

// Serialize marshals the BulletProof into its byte representation.
func (p *BulletProof) Serialize() ([]byte, error) {
	return asn1.Marshal[asn1.Serializer](p.SameType, p.RangeCorrectness)
}

// Deserialize unmarshals the BulletProof from its byte representation.
func (p *BulletProof) Deserialize(bytes []byte) error {
	p.SameType = &SameType{}
	p.RangeCorrectness = &bulletproof.RangeCorrectness{}

	if err := asn1.Unmarshal[asn1.Serializer](bytes, p.SameType, p.RangeCorrectness); err != nil {
		return errors.Join(ErrDeserializeProofFailed, err)
	}

	return nil
}

// BulletProofProver produces a proof of validity for an IssueAction.
type BulletProofProver struct {
	// SameType is the prover for the same-type property.
	SameType *SameTypeProver
	// RangeCorrectness is the prover for the range correctness property.
	RangeCorrectness *bulletproof.RangeCorrectnessProver
}

// NewBulletProofProver instantiates a BulletProofProver for an issue action using the provided witnesses, tokens, and public parameters.
func NewBulletProofProver(tw []*token.Metadata, tokens []*math.G1, pp *v1.PublicParams) (*BulletProofProver, error) {
	c := math.Curves[pp.Curve]
	p := &BulletProofProver{}
	tokenType := c.HashToZr([]byte(tw[0].Type))
	commitmentToType := pp.PedersenGenerators[0].Mul(tokenType)

	rand, err := c.Rand()
	if err != nil {
		return nil, errors.Join(ErrGetIssueProverFailed, err)
	}
	typeBF := c.NewRandomZr(rand)
	commitmentToType.Add(pp.PedersenGenerators[2].Mul(typeBF))
	p.SameType = NewSameTypeProver(tw[0].Type, typeBF, commitmentToType, pp.PedersenGenerators, c)

	values := make([]uint64, len(tw))
	blindingFactors := make([]*math.Zr, len(tw))
	for i := range tw {
		if tw[i] == nil || tw[i].BlindingFactor == nil {
			return nil, ErrInvalidTokenWitness
		}
		// tw[i] = tw[i].Clone()
		values[i], err = tw[i].Value.Uint()
		if err != nil {
			return nil, errors.Join(ErrInvalidTokenWitnessValues, err)
		}
		blindingFactors[i] = c.ModSub(tw[i].BlindingFactor, p.SameType.blindingFactor, c.GroupOrder)
	}
	coms := make([]*math.G1, len(tokens))
	for i := range tokens {
		coms[i] = tokens[i].Copy()
		coms[i].Sub(commitmentToType)
	}
	// The range prover takes commitments to values (tokens[i] / commitmentToType).
	p.RangeCorrectness = bulletproof.NewRangeCorrectnessProver(
		coms,
		values,
		blindingFactors,
		pp.PedersenGenerators[1:],
		pp.RangeProofParams.LeftGenerators,
		pp.RangeProofParams.RightGenerators,
		pp.RangeProofParams.P,
		pp.RangeProofParams.Q,
		pp.RangeProofParams.BitLength,
		pp.RangeProofParams.NumberOfRounds,
		math.Curves[pp.Curve],
		pp.ExecutorProvider,
	)

	return p, nil
}

// Prove generates the zero-knowledge proof of validity.
func (p *BulletProofProver) Prove() ([]byte, error) {
	// Generate same-type proof.
	st, err := p.SameType.Prove()
	if err != nil {
		return nil, errors.Join(ErrGenerateIssueProofFailed, err)
	}

	// Generate range correctness proof.
	rc, err := p.RangeCorrectness.Prove()
	if err != nil {
		return nil, errors.Join(ErrGenerateRangeProofFailed, err)
	}

	proof := &BulletProof{
		SameType:         st,
		RangeCorrectness: rc,
	}

	return proof.Serialize()
}

// RangeProofType returns the type of range proof used by this prover.
func (p *BulletProofProver) RangeProofType() rp.ProofType {
	return rp.RangeProofType
}

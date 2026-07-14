/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp/csp"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// CSPProof proves that an IssueAction is valid by demonstrating that all issued tokens
// have the same type and that their values are within the authorized range.
// It uses CSP-based range proofs instead of BulletProofs.
type CSPProof struct {
	// SameType is the proof that all issued tokens have the same type.
	SameType *SameType
	// RangeCorrectness is the proof that issued tokens have values in the authorized range.
	RangeCorrectness *csp.RangeCorrectness
}

// Serialize marshals the CSPProof into its byte representation.
func (p *CSPProof) Serialize() ([]byte, error) {
	return asn1.Marshal[asn1.Serializer](p.SameType, p.RangeCorrectness)
}

// Deserialize unmarshals the CSPProof from its byte representation.
func (p *CSPProof) Deserialize(bytes []byte) error {
	p.SameType = &SameType{}
	p.RangeCorrectness = &csp.RangeCorrectness{}

	if err := asn1.Unmarshal[asn1.Serializer](bytes, p.SameType, p.RangeCorrectness); err != nil {
		return errors.Join(ErrDeserializeProofFailed, err)
	}

	return nil
}

// CSPBasedProver produces a CSP-based proof of validity for an IssueAction.
type CSPBasedProver struct {
	// SameType is the prover for the same-type property.
	SameType *SameTypeProver
	// RangeCorrectness is the prover for the range correctness property.
	RangeCorrectness *csp.RangeCorrectnessProver
}

// NewCSPBasedProver instantiates a CSPBasedProver for an issue action using the provided witnesses, tokens, and public parameters.
func NewCSPBasedProver(tw []*token.Metadata, tokens []*math.G1, pp *v1.PublicParams) (*CSPBasedProver, error) {
	c := math.Curves[pp.Curve]
	p := &CSPBasedProver{}
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
	p.RangeCorrectness = csp.NewRangeCorrectnessProver(
		coms,
		values,
		blindingFactors,
		pp.PedersenGenerators[1:],
		pp.CSPRangeProofParams.LeftGenerators,
		pp.CSPRangeProofParams.RightGenerators,
		pp.CSPRangeProofParams.BitLength,
		math.Curves[pp.Curve],
		pp.ExecutorProvider,
	).WithTranscriptHeader(pp.CSPRangeProofParams.RPTranscriptHeader)

	return p, nil
}

// Prove generates the zero-knowledge proof of validity.
func (p *CSPBasedProver) Prove() ([]byte, error) {
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

	proof := &CSPProof{
		SameType:         st,
		RangeCorrectness: rc,
	}

	return proof.Serialize()
}

// RangeProofType returns the type of range proof used by this prover.
func (p *CSPBasedProver) RangeProofType() rp.ProofType {
	return rp.CSPRangeProofType
}

// CSPVerifier coordinates the verification of CSP-based zero-knowledge proofs for an issue action.
type CSPVerifier struct {
	// SameType is the verifier for the same-type property.
	SameType *SameTypeVerifier
	// RangeCorrectness is the verifier for the range correctness property.
	RangeCorrectness *csp.RangeCorrectnessVerifier
}

// NewCSPVerifier instantiates a CSPVerifier for the given token commitments and public parameters.
func NewCSPVerifier(tokens []*math.G1, pp *v1.PublicParams) *CSPVerifier {
	v := &CSPVerifier{}
	v.SameType = NewSameTypeVerifier(tokens, pp.PedersenGenerators, math.Curves[pp.Curve])
	v.RangeCorrectness = csp.NewRangeCorrectnessVerifier(
		pp.PedersenGenerators[1:],
		pp.CSPRangeProofParams.LeftGenerators,
		pp.CSPRangeProofParams.RightGenerators,
		pp.CSPRangeProofParams.BitLength,
		math.Curves[pp.Curve],
		pp.ExecutorProvider,
	).WithTranscriptHeader(pp.CSPRangeProofParams.RPTranscriptHeader)

	return v
}

// Verify checks the validity of the zero-knowledge proof for an issue action.
// It verifies both the same-type property and the range correctness of the issued tokens.
func (v *CSPVerifier) Verify(proof []byte) error {
	tp := &CSPProof{}
	// Unmarshal the proof.
	err := tp.Deserialize(proof)
	if err != nil {
		return errors.Join(ErrDeserializeProofFailed, err)
	}
	// Verify the same-type proof.
	err = v.SameType.Verify(tp.SameType)
	if err != nil {
		return errors.Join(ErrInvalidIssueProof, err)
	}
	// Verify the range correctness proof.
	// The range proof is performed on tokens[i] / commitmentToType to show they commit to a positive value.
	commitmentToType := tp.SameType.CommitmentToType.Copy()
	coms := make([]*math.G1, len(v.SameType.Tokens))
	for i := range len(v.SameType.Tokens) {
		coms[i] = v.SameType.Tokens[i].Copy()
		coms[i].Sub(commitmentToType)
	}
	v.RangeCorrectness.Commitments = coms
	err = v.RangeCorrectness.Verify(tp.RangeCorrectness)
	if err != nil {
		return errors.Join(ErrInvalidIssueProof, err)
	}

	return nil
}

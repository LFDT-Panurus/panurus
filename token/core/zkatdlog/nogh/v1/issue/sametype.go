/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/asn1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// SameType is a zero-knowledge proof that all issued tokens have the same type
// and that the type is properly committed in each token's Pedersen commitment.
type SameType struct {
	// Type is the proof response for the token type.
	Type *math.Zr
	// BlindingFactor is the proof response for the blinding factor used in the type commitment.
	BlindingFactor *math.Zr
	// Challenge is the Fiat-Shamir challenge for the proof.
	Challenge *math.Zr
	// CommitmentToType is the commitment to the type being issued.
	CommitmentToType *math.G1
}

// Serialize marshals the SameType proof into its byte representation.
func (stp *SameType) Serialize() ([]byte, error) {
	return asn1.MarshalMath(
		stp.Type,
		stp.BlindingFactor,
		stp.Challenge,
		stp.CommitmentToType,
	)
}

// Deserialize unmarshals the SameType proof from its byte representation.
func (stp *SameType) Deserialize(bytes []byte) error {
	unmarshaller, err := asn1.NewUnmarshaller(bytes)
	if err != nil {
		return errors.Join(ErrUnmarshalSameTypeFailed, err)
	}
	stp.Type, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(ErrDeserializeTypeFailed, err)
	}
	stp.BlindingFactor, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(ErrDeserializeBlindingFactorFailed, err)
	}
	stp.Challenge, err = unmarshaller.NextZr()
	if err != nil {
		return errors.Join(ErrDeserializeChallengeFailed, err)
	}
	stp.CommitmentToType, err = unmarshaller.NextG1()
	if err != nil {
		return errors.Join(ErrDeserializeCommitmentToTypeFailed, err)
	}

	return nil
}

// SameTypeRandomness holds the secret randomness used during the proof generation.
type SameTypeRandomness struct {
	blindingFactor *math.Zr
	tokenType      *math.Zr
}

// SameTypeProver generates a proof that all issued tokens have the same type.
type SameTypeProver struct {
	// PedParams are the generators for Pedersen commitments.
	PedParams []*math.G1
	// Curve is the elliptic curve used for the proof.
	Curve *math.Curve
	// tokenType is the type of the tokens being issued.
	tokenType token2.Type
	// blindingFactor is the blinding factor in the CommitmentToType.
	blindingFactor *math.Zr
	// CommitmentToType is the commitment to the token type.
	CommitmentToType *math.G1
	// randomness is the secret randomness used for proof generation.
	randomness *SameTypeRandomness
	// commitment is the commitment to the randomness.
	commitment *math.G1
}

// NewSameTypeProver returns a new SameTypeProver instance.
func NewSameTypeProver(ttype token2.Type, bf *math.Zr, com *math.G1, pp []*math.G1, c *math.Curve) *SameTypeProver {
	return &SameTypeProver{
		tokenType:        ttype,
		blindingFactor:   bf,
		CommitmentToType: com,
		PedParams:        pp,
		Curve:            c,
	}
}

// Prove generates the SameType proof.
func (p *SameTypeProver) Prove() (*SameType, error) {
	tokenType := p.Curve.HashToZr([]byte(p.tokenType))

	// Compute commitment to the randomness.
	err := p.computeCommitment()
	if err != nil {
		return nil, errors.Join(ErrProveTypeFailed, err)
	}
	array := common.GetG1Array([]*math.G1{p.CommitmentToType, p.commitment})
	var toHash []byte
	toHash, err = array.Bytes()
	if err != nil {
		return nil, errors.Join(ErrProveTypeFailed, err)
	}
	// Compute the challenge using Fiat-Shamir.
	chal := p.Curve.HashToZr(toHash)
	proof := &SameType{
		CommitmentToType: p.CommitmentToType,
		Challenge:        chal,
	}
	// Compute the proof responses.
	proof.Type = p.Curve.ModMul(chal, tokenType, p.Curve.GroupOrder)
	proof.Type = p.Curve.ModAdd(proof.Type, p.randomness.tokenType, p.Curve.GroupOrder)

	proof.BlindingFactor = p.Curve.ModMul(chal, p.blindingFactor, p.Curve.GroupOrder)
	proof.BlindingFactor = p.Curve.ModAdd(proof.BlindingFactor, p.randomness.blindingFactor, p.Curve.GroupOrder)

	return proof, nil
}

// computeCommitment generates randomness and computes the commitment to it.
func (p *SameTypeProver) computeCommitment() error {
	rand, err := p.Curve.Rand()
	if err != nil {
		return ErrGetRNGFailed
	}
	p.randomness = &SameTypeRandomness{}
	p.randomness.tokenType = p.Curve.NewRandomZr(rand)
	p.randomness.blindingFactor = p.Curve.NewRandomZr(rand)

	p.commitment = p.PedParams[0].Mul(p.randomness.tokenType)
	p.commitment.Add(p.PedParams[2].Mul(p.randomness.blindingFactor))

	return nil
}

// SameTypeVerifier verifies a SameType proof.
type SameTypeVerifier struct {
	// PedParams are the generators for Pedersen commitments.
	PedParams []*math.G1
	// Curve is the elliptic curve used for verification.
	Curve *math.Curve
	// Tokens are the commitments to the issued tokens.
	Tokens []*math.G1
}

// NewSameTypeVerifier returns a new SameTypeVerifier instance.
func NewSameTypeVerifier(tokens []*math.G1, pp []*math.G1, c *math.Curve) *SameTypeVerifier {
	return &SameTypeVerifier{
		Tokens:    tokens,
		PedParams: pp,
		Curve:     c,
	}
}

// Verify checks the validity of the SameType proof.
func (v *SameTypeVerifier) Verify(proof *SameType) error {
	// Recompute the commitment to randomness from the proof responses.
	com := v.PedParams[0].Mul(proof.Type)
	com.Add(v.PedParams[2].Mul(proof.BlindingFactor))
	com.Sub(proof.CommitmentToType.Mul(proof.Challenge))

	// Recompute the challenge and check it matches the one in the proof.
	raw, err := common.GetG1Array([]*math.G1{proof.CommitmentToType, com}).Bytes()
	if err != nil {
		return errors.Join(ErrVerifySameTypeProofFailed, err)
	}

	if !v.Curve.HashToZr(raw).Equals(proof.Challenge) {
		return ErrInvalidSameTypeProof
	}

	return nil
}

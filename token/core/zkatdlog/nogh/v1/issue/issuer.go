/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import (
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/common"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

//go:generate counterfeiter -o mock/signing_identity.go -fake-name SigningIdentity . SigningIdentity
type SigningIdentity interface {
	driver.SigningIdentity
}

// Issuer is the entity responsible for creating and signing issue actions.
type Issuer struct {
	// Signer is the identity used to sign the issue action.
	Signer SigningIdentity
	// PublicParams contains the public parameters for the ZK proofs.
	PublicParams *v1.PublicParams
	// Type is the type of token being issued.
	Type token2.Type
}

// NewIssuer returns a new Issuer instance with the specified token type, signer, and public parameters.
func NewIssuer(tokenType token2.Type, signer common.SigningIdentity, pp *v1.PublicParams) *Issuer {
	return &Issuer{
		Signer:       signer,
		Type:         tokenType,
		PublicParams: pp,
	}
}

// GenerateZKIssue creates a zero-knowledge issue action for the given values and owners.
// It returns the issue action, metadata for each issued token, or an error if the process fails.
func (i *Issuer) GenerateZKIssue(values []uint64, owners [][]byte) (*Action, []*token.Metadata, error) {
	if i.PublicParams == nil {
		return nil, nil, ErrNilPublicParameters
	}
	if len(math.Curves) < int(i.PublicParams.Curve)+1 {
		return nil, nil, ErrInvalidPublicParameters
	}
	// Generate tokens with their corresponding witnesses (value and blinding factor).
	tokens, tw, err := token.GetTokensWithWitness(values, i.Type, i.PublicParams.PedersenGenerators, math.Curves[i.PublicParams.Curve])
	if err != nil {
		return nil, nil, errors.Join(ErrGetTokensFailed, err)
	}

	// Create a prover and generate the zero-knowledge proof of validity.
	prover, err := NewProver(tw, tokens, i.PublicParams)
	if err != nil {
		return nil, nil, errors.Join(ErrGetIssueProverFailed, err)
	}
	proof, err := prover.Prove()
	if err != nil {
		return nil, nil, ErrGenerateZKProof
	}

	if i.Signer == nil {
		return nil, nil, ErrNilSigner
	}
	signerRaw, err := i.Signer.Serialize()
	if err != nil {
		return nil, nil, errors.Join(ErrSerializeSignerFailed, err)
	}

	// Create the issue action.
	issue, err := NewAction(signerRaw, tokens, owners, proof)
	if err != nil {
		return nil, nil, errors.Join(ErrCreateActionFailed, err)
	}
	// Set the proof type based on the prover used
	issue.ProofType = prover.RangeProofType()

	// Prepare metadata for each issued token.
	inf := make([]*token.Metadata, len(values))
	for j := range inf {
		inf[j] = &token.Metadata{
			Type:           i.Type,
			Value:          tw[j].Value,
			BlindingFactor: tw[j].BlindingFactor,
			Issuer:         signerRaw,
		}
	}

	return issue, inf, nil
}

// SignTokenActions signs the serialized token actions using the issuer's signer.
func (i *Issuer) SignTokenActions(raw []byte) ([]byte, error) {
	if i.Signer == nil {
		return nil, ErrSignTokenActionsNilSigner
	}

	return i.Signer.Sign(raw)
}

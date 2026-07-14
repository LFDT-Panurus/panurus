/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/token"
)

//go:generate counterfeiter -o mock/cc.go -fake-name CertificationClient . CertificationClient

// CertificationClient provides methods to check token certification status and request new certifications.
type CertificationClient interface {
	// IsCertified checks if a specific token identifier has already been certified.
	IsCertified(ctx context.Context, id *token.ID) bool
	// RequestCertification initiates the certification process for the given list of token identifiers.
	RequestCertification(ctx context.Context, ids ...*token.ID) error
}

//go:generate counterfeiter -o mock/cs.go -fake-name CertificationService . CertificationService

// CertificationService handles the generation and verification of token certifications.
// Certifications provide proofs of a token's validity and status, often used in
// complex transaction scenarios or cross-network interactions.
type CertificationService interface {
	// NewCertificationRequest creates a serialized certification request for the specified token identifiers.
	NewCertificationRequest(ids []*token.ID) ([]byte, error)

	// Certify uses the provided certifier wallet and ledger representations to certify a list of token identifiers.
	// It takes as input the certification request and the raw token data as stored on the ledger.
	Certify(wallet CertifierWallet, ids []*token.ID, tokens [][]byte, request []byte) ([][]byte, error)

	// VerifyCertifications validates the provided certifications for a given list of token identifiers.
	// It returns the processed certifications if they are valid, or an error otherwise.
	VerifyCertifications(ids []*token.ID, certifications [][]byte) ([][]byte, error)
}

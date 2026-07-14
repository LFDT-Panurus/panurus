/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Identity represents a generic identity
type Identity = driver.Identity

// Verifier models a signature verifier
type Verifier = driver.Verifier

// Signer models a signature signer
type Signer = driver.Signer

// SignatureService gives access to signature verifiers and signers bound to identities known by
// this service
type SignatureService struct {
	deserializer     driver.Deserializer
	identityProvider driver.IdentityProvider
}

// NewSignatureService returns a instance of SignatureService
func NewSignatureService(deserializer driver.Deserializer, identityProvider driver.IdentityProvider) *SignatureService {
	return &SignatureService{deserializer: deserializer, identityProvider: identityProvider}
}

// AuditorVerifier returns a signature verifier for the given auditor identity
func (s *SignatureService) AuditorVerifier(ctx context.Context, id Identity) (Verifier, error) {
	return s.deserializer.GetAuditorVerifier(ctx, id)
}

// OwnerVerifier returns a signature verifier for the given owner identity
func (s *SignatureService) OwnerVerifier(ctx context.Context, id Identity) (Verifier, error) {
	return s.deserializer.GetOwnerVerifier(ctx, id)
}

// IssuerVerifier returns a signature verifier for the given issuer identity
func (s *SignatureService) IssuerVerifier(ctx context.Context, id Identity) (Verifier, error) {
	return s.deserializer.GetIssuerVerifier(ctx, id)
}

// GetSigner returns a signer bound to the given identity
func (s *SignatureService) GetSigner(ctx context.Context, id Identity) (Signer, error) {
	return s.identityProvider.GetSigner(ctx, id)
}

// RegisterSigner registers the pair (signer, verifier) bound to the given identity
func (s *SignatureService) RegisterSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	return s.identityProvider.RegisterSigner(ctx, identity, signer, verifier, nil, false)
}

// RegisterEphemeralSigner registers the pair (signer, verifier) bound to the given identity only in memory
func (s *SignatureService) RegisterEphemeralSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	return s.identityProvider.RegisterSigner(ctx, identity, signer, verifier, nil, true)
}

// AreMe returns the hashes of the passed identities that have a signer registered before
func (s *SignatureService) AreMe(ctx context.Context, identities ...Identity) []string {
	return s.identityProvider.AreMe(ctx, identities...)
}

// IsMe returns true if for the given identity there is a signer registered
func (s *SignatureService) IsMe(ctx context.Context, party Identity) bool {
	return s.identityProvider.IsMe(ctx, party)
}

// GetAuditInfo returns the audit infor
func (s *SignatureService) GetAuditInfo(ctx context.Context, ids ...Identity) ([][]byte, error) {
	result := make([][]byte, 0, len(ids))
	for _, id := range ids {
		auditInfo, err := s.identityProvider.GetAuditInfo(ctx, id)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get audit info for identity [%s]", id)
		}
		result = append(result, auditInfo)
	}

	return result, nil
}

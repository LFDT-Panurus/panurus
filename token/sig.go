/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
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
//
// Verification: see checkSignerIdentity. The identity must be non-empty and must
// be one this driver can derive a verifier for.
func (s *SignatureService) RegisterSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	if err := s.checkSignerIdentity(ctx, identity); err != nil {
		return errors.WithMessage(err, "refusing to register signer")
	}

	return s.identityProvider.RegisterSigner(ctx, identity, signer, verifier, nil, false)
}

// RegisterEphemeralSigner registers the pair (signer, verifier) bound to the given identity only in memory
//
// Verification: as for RegisterSigner. An ephemeral registration never reaches
// storage but still populates the in-memory signer cache, which is keyed the
// same way, so it is held to the same conditions.
func (s *SignatureService) RegisterEphemeralSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	if err := s.checkSignerIdentity(ctx, identity); err != nil {
		return errors.WithMessage(err, "refusing to register ephemeral signer")
	}

	return s.identityProvider.RegisterSigner(ctx, identity, signer, verifier, nil, true)
}

// checkSignerIdentity is the check applied before a signer is bound to an
// identity.
//
// It enforces two conditions. The identity must be non-empty, because identities
// are keyed by unique id and the unique id of the empty identity is a fixed
// string rather than a hash — every empty identity would share one cache and
// storage key, so a signer registered for one would be returned for any other.
// And the identity must be one this driver can derive a verifier for: signers are
// registered for identities that arrive from a remote party (see the recipient
// and multisig flows in token/services/ttx), and binding a signer to bytes no
// verifier can be built from produces an identity that can sign but whose
// signatures nothing can check.
//
// The owner role is the one asked, and asking it is enough: every driver in the
// tree builds common.NewDeserializer from a single
// TypedVerifierDeserializerMultiplex, so the owner, issuer and auditor
// deserializers are the same object and route by identity type rather than by
// role. Trying the other two would deserialize the same identity again for the
// same answer, on a path that runs per recipient per transaction.
//
// What this deliberately does not do is check the supplied verifier against the
// identity. driver.Verifier exposes only Verify(message, sigma), with no
// canonical public key to compare, so establishing agreement would require a new
// accessor on every identity type. The in-tree callers that pass a verifier are
// the x509 and idemix key managers, which derive it from the identity they are
// registering, so the comparison would be a tautology there; the ttx callers
// pass nil. See docs/security/store_integrity_verification.md.
func (s *SignatureService) checkSignerIdentity(ctx context.Context, identity Identity) error {
	if err := integrity.CheckIdentity(identity); err != nil {
		return err
	}
	if _, err := s.deserializer.GetOwnerVerifier(ctx, identity); err != nil {
		return errors.Wrapf(err, "failed to derive a verifier for identity [%s]", identity)
	}

	return nil
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

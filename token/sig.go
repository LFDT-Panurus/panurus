/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// SignatureThrottled is the contract error returned (directly or wrapped) when a signature
// operation is denied because the requesting principal has exceeded its quota or is currently
// blocked by the throttle policy. Callers detect it with errors.Is to tell "you are asking too
// often" apart from "this identity is unknown" or "this signature is invalid", which is the
// difference between backing off and giving up.
var SignatureThrottled = errors.New("signature operation rate limit exceeded")

// Identity represents a generic identity
type Identity = driver.Identity

// Verifier models a signature verifier
type Verifier = driver.Verifier

// Signer models a signature signer
type Signer = driver.Signer

// SignatureGate decides whether a signature operation on behalf of a principal may proceed. It
// is the seam through which a throttle policy is installed in front of the signature surface;
// package token deliberately depends on the interface only, so no policy implementation is
// pulled into the client-facing API.
//
// Implementations must be safe for concurrent use and must not block. Denials must return an
// error that satisfies errors.Is(err, SignatureThrottled).
type SignatureGate = sigobserve.Gate

// SignatureService gives access to signature verifiers and signers bound to identities known by
// this service
type SignatureService struct {
	deserializer     driver.Deserializer
	identityProvider driver.IdentityProvider

	// observer receives the events this service produces. It only reports denials: the
	// operations themselves are instrumented where they happen, in the identity provider and
	// in the deserializer, so that calls arriving through other entry points are observed too
	// and no operation is counted twice.
	observer sigobserve.Observer
	// gate, when set, may deny an operation before it runs.
	gate SignatureGate
}

// SignatureServiceOption customizes a SignatureService.
type SignatureServiceOption func(*SignatureService)

// WithSignatureObserver installs the observer that denied operations are reported to.
func WithSignatureObserver(o sigobserve.Observer) SignatureServiceOption {
	return func(s *SignatureService) {
		if o != nil {
			s.observer = o
		}
	}
}

// WithSignatureGate installs the gate consulted before each signature operation.
func WithSignatureGate(g SignatureGate) SignatureServiceOption {
	return func(s *SignatureService) { s.gate = g }
}

// NewSignatureService returns a instance of SignatureService
func NewSignatureService(deserializer driver.Deserializer, identityProvider driver.IdentityProvider, opts ...SignatureServiceOption) *SignatureService {
	s := &SignatureService{
		deserializer:     deserializer,
		identityProvider: identityProvider,
		observer:         sigobserve.Nop,
	}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// AuditorVerifier returns a signature verifier for the given auditor identity.
//
// This operation is not gated: the identity always comes from the public parameters of the
// token system, so the set of principals is tiny, fixed and not attacker-controlled. Applying
// the rate-limit quota here would make DefaultRate a hard ceiling on transaction throughput
// for the node, not a per-counterparty abuse limit. The operation is still instrumented
// downstream in the deserializer.
func (s *SignatureService) AuditorVerifier(ctx context.Context, id Identity) (Verifier, error) {
	return s.deserializer.GetAuditorVerifier(ctx, id)
}

// OwnerVerifier returns a signature verifier for the given owner identity
func (s *SignatureService) OwnerVerifier(ctx context.Context, id Identity) (Verifier, error) {
	if err := s.allow(ctx, sigobserve.OpOwnerVerifier, sigobserve.RoleOwner, id); err != nil {
		return nil, err
	}

	return s.deserializer.GetOwnerVerifier(ctx, id)
}

// IssuerVerifier returns a signature verifier for the given issuer identity
func (s *SignatureService) IssuerVerifier(ctx context.Context, id Identity) (Verifier, error) {
	if err := s.allow(ctx, sigobserve.OpIssuerVerifier, sigobserve.RoleIssuer, id); err != nil {
		return nil, err
	}

	return s.deserializer.GetIssuerVerifier(ctx, id)
}

// GetSigner returns a signer bound to the given identity.
//
// This operation is not gated: on the hot endorsement path it is called with the node's own
// long-term signing identity, so all of a node's traffic would be charged to a single bucket
// and DefaultRate would become a global TPS cap on endorsements. The operation is still
// instrumented downstream in the identity provider.
func (s *SignatureService) GetSigner(ctx context.Context, id Identity) (Signer, error) {
	return s.identityProvider.GetSigner(ctx, id)
}

// RegisterSigner registers the pair (signer, verifier) bound to the given identity
func (s *SignatureService) RegisterSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	if err := s.allow(ctx, sigobserve.OpRegisterSigner, sigobserve.RoleUnknown, identity); err != nil {
		return err
	}

	return s.identityProvider.RegisterSigner(ctx, identity, signer, verifier, nil, false)
}

// RegisterEphemeralSigner registers the pair (signer, verifier) bound to the given identity only in memory
func (s *SignatureService) RegisterEphemeralSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	if err := s.allow(ctx, sigobserve.OpRegisterSigner, sigobserve.RoleUnknown, identity); err != nil {
		return err
	}

	return s.identityProvider.RegisterSigner(ctx, identity, signer, verifier, nil, true)
}

// AreMe returns the hashes of the passed identities that have a signer registered before
//
// The operation is not gated: it answers a question about local state and cannot report a
// denial, and returning "not mine" for an identity that is in fact ours would be a wrong
// answer rather than a refusal.
func (s *SignatureService) AreMe(ctx context.Context, identities ...Identity) []string {
	return s.identityProvider.AreMe(ctx, identities...)
}

// IsMe returns true if for the given identity there is a signer registered
//
// As with AreMe, the operation is not gated: false would be a wrong answer, not a refusal.
func (s *SignatureService) IsMe(ctx context.Context, party Identity) bool {
	return s.identityProvider.IsMe(ctx, party)
}

// GetAuditInfo returns the audit infor
func (s *SignatureService) GetAuditInfo(ctx context.Context, ids ...Identity) ([][]byte, error) {
	result := make([][]byte, 0, len(ids))
	for _, id := range ids {
		if err := s.allow(ctx, sigobserve.OpGetAuditInfo, sigobserve.RoleUnknown, id); err != nil {
			return nil, err
		}
		auditInfo, err := s.identityProvider.GetAuditInfo(ctx, id)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get audit info for identity [%s]", id)
		}
		result = append(result, auditInfo)
	}

	return result, nil
}

// allow consults the gate and reports a denial as a throttled event. It returns nil when no
// gate is installed, so an unconfigured service behaves exactly as before.
func (s *SignatureService) allow(ctx context.Context, op sigobserve.Op, role sigobserve.Role, id Identity) error {
	if s.gate == nil {
		return nil
	}

	principal := id.UniqueID()
	if err := s.gate.Allow(ctx, principal, op); err != nil {
		sigobserve.Start(s.observer, op, principal, role).DoneThrottled(ctx, err)

		return err
	}

	return nil
}

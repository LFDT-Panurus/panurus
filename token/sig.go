/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
	"github.com/LFDT-Panurus/panurus/token/services/storage/integrity"
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

// PrincipalKeyResolver maps an identity to the key its throttle quota is tracked under.
//
// By default the gate keys on the identity hash (Identity.UniqueID()). For a party that presents
// a fresh pseudonym per request — an Idemix nym on the recipient path is the canonical case —
// every request is then a new key with a full quota, so pseudonym rotation walks straight around
// the per-principal limit. A resolver folds those rotating pseudonyms onto one stable key (an
// enrollment ID, a session or peer identity) so the quota follows the party rather than the
// pseudonym.
//
// ThrottleKey must be safe for concurrent use and must not block; it is on the signature path.
// Returning "" means "no stable key available" and the gate falls back to the identity hash — the
// safe default, since an over-broad key would let unrelated identities throttle each other. A
// resolver that cannot cheaply resolve an identity (for example an unregistered pseudonym with no
// locally known audit info) must return "" rather than perform expensive lookups on the hot path.
type PrincipalKeyResolver interface {
	// ThrottleKey returns the stable throttle key for id, or "" to fall back to id.UniqueID().
	ThrottleKey(ctx context.Context, id Identity) string
}

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
	// principalKey, when set, resolves the stable throttle key for an identity so that a party's
	// rotating pseudonyms are charged to one quota. When nil, or when it returns "", the gate
	// keys on the identity hash.
	principalKey PrincipalKeyResolver
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

// WithPrincipalKeyResolver installs the resolver that maps an identity to the stable key its
// throttle quota is tracked under. Without it the gate keys on the identity hash, so a party
// presenting a fresh pseudonym per request is never throttled. See PrincipalKeyResolver.
func WithPrincipalKeyResolver(r PrincipalKeyResolver) SignatureServiceOption {
	return func(s *SignatureService) {
		if r != nil {
			s.principalKey = r
		}
	}
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
//
// Verification: see checkSignerIdentity. The identity must be non-empty and must
// be one this driver can derive a verifier for.
func (s *SignatureService) RegisterSigner(ctx context.Context, identity Identity, signer Signer, verifier Verifier) error {
	if err := s.allow(ctx, sigobserve.OpRegisterSigner, sigobserve.RoleUnknown, identity); err != nil {
		return err
	}
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
	if err := s.allow(ctx, sigobserve.OpRegisterSigner, sigobserve.RoleUnknown, identity); err != nil {
		return err
	}
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

// AreMe returns the hashes of the passed identities that have a signer registered before.
// A non-nil error means the ownership check could not be completed; in that case the returned
// slice is nil, not a partial answer, and a caller must not mistake "couldn't check" for
// "confirmed not mine".
//
// The operation is not gated: it answers a question about local state and cannot report a
// denial, and returning "not mine" for an identity that is in fact ours would be a wrong
// answer rather than a refusal.
func (s *SignatureService) AreMe(ctx context.Context, identities ...Identity) ([]string, error) {
	return s.identityProvider.AreMe(ctx, identities...)
}

// IsMe returns true if for the given identity there is a signer registered.
// A non-nil error means ownership could not be determined; the boolean must be ignored in
// that case rather than treated as an authoritative "not mine".
//
// As with AreMe, the operation is not gated: false would be a wrong answer, not a refusal.
func (s *SignatureService) IsMe(ctx context.Context, party Identity) (bool, error) {
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

	// Key on the party's stable throttle key when a resolver can supply one, so that rotating
	// pseudonyms are charged to a single quota; otherwise fall back to the identity hash. The
	// resolver is consulted only when a gate is installed, so an ungated service pays nothing.
	principal := id.UniqueID()
	if s.principalKey != nil {
		if key := s.principalKey.ThrottleKey(ctx, id); key != "" {
			principal = key
		}
	}
	if err := s.gate.Allow(ctx, principal, op); err != nil {
		sigobserve.Start(s.observer, op, principal, role).DoneThrottled(ctx, err)

		return err
	}

	return nil
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/sigobserve"
)

// Deserializer deserializes verifiers associated with issuers, owners, and auditors
type Deserializer struct {
	auditorDeserializer  driver.VerifierDeserializer
	ownerDeserializer    driver.VerifierDeserializer
	issuerDeserializer   driver.VerifierDeserializer
	auditMatcherProvider driver.AuditMatcherProvider
	recipientExtractor   driver.RecipientExtractor

	// observer receives one event per verifier resolution, and one per Verify performed with a
	// resolved verifier. It defaults to a no-op, so an unwired deserializer costs nothing.
	observer sigobserve.Observer
}

// NewDeserializer returns a new Deserializer for the passed arguments.
func NewDeserializer(
	auditorDeserializer driver.VerifierDeserializer,
	ownerDeserializer driver.VerifierDeserializer,
	issuerDeserializer driver.VerifierDeserializer,
	auditMatcherProvider driver.AuditMatcherProvider,
	recipientExtractor driver.RecipientExtractor,
) *Deserializer {
	return &Deserializer{
		auditorDeserializer:  auditorDeserializer,
		ownerDeserializer:    ownerDeserializer,
		issuerDeserializer:   issuerDeserializer,
		auditMatcherProvider: auditMatcherProvider,
		recipientExtractor:   recipientExtractor,
		observer:             sigobserve.Nop,
	}
}

// SetObserver installs the observer that verifier resolutions, and the verifications performed
// with the resolved verifiers, are reported to. Passing nil restores the no-op observer.
//
// It is a setter rather than a constructor parameter because a deserializer is built by every
// driver and by the validators, and only the ones a node builds for its own client-facing
// services have an observer to give.
func (d *Deserializer) SetObserver(o sigobserve.Observer) {
	if o == nil {
		o = sigobserve.Nop
	}
	d.observer = o
}

// GetOwnerVerifier returns the verifier associated to the passed owner identity.
func (d *Deserializer) GetOwnerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return d.getVerifier(ctx, d.ownerDeserializer, id, sigobserve.OpOwnerVerifier, sigobserve.RoleOwner)
}

// GetIssuerVerifier returns the verifier associated to the passed issuer identity.
func (d *Deserializer) GetIssuerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return d.getVerifier(ctx, d.issuerDeserializer, id, sigobserve.OpIssuerVerifier, sigobserve.RoleIssuer)
}

// GetAuditorVerifier returns the verifier associated to the passed auditor identity.
func (d *Deserializer) GetAuditorVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return d.getVerifier(ctx, d.auditorDeserializer, id, sigobserve.OpAuditorVerifier, sigobserve.RoleAuditor)
}

// getVerifier resolves a verifier through vd, reports the resolution as op, and wraps the result
// so that the verifications it performs are reported too.
func (d *Deserializer) getVerifier(ctx context.Context, vd driver.VerifierDeserializer, id driver.Identity, op sigobserve.Op, role sigobserve.Role) (driver.Verifier, error) {
	principal := id.UniqueID()
	t := sigobserve.Start(d.observer, op, principal, role)
	verifier, err := vd.DeserializeVerifier(ctx, id)
	t.Done(ctx, err)

	return sigobserve.InstrumentVerifier(verifier, d.observer, principal, role), err
}

// Recipients returns the recipient identities extracted from the passed identity.
func (d *Deserializer) Recipients(id driver.Identity) ([]driver.Identity, error) {
	return d.recipientExtractor.Recipients(id)
}

// GetAuditInfoMatcher returns an identity matcher for the passed identity and audit info.
func (d *Deserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return d.auditMatcherProvider.GetAuditInfoMatcher(ctx, owner, auditInfo)
}

// MatchIdentity returns nil if the given identity matches the given audit information.
func (d *Deserializer) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	return d.auditMatcherProvider.MatchIdentity(ctx, id, ai)
}

// GetAuditInfo returns the audit information for the passed identity, if available.
func (d *Deserializer) GetAuditInfo(ctx context.Context, id driver.Identity, p driver.AuditInfoProvider) ([]byte, error) {
	return d.auditMatcherProvider.GetAuditInfo(ctx, id, p)
}

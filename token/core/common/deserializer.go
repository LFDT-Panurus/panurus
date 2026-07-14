/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
)

// Deserializer deserializes verifiers associated with issuers, owners, and auditors
type Deserializer struct {
	auditorDeserializer  driver.VerifierDeserializer
	ownerDeserializer    driver.VerifierDeserializer
	issuerDeserializer   driver.VerifierDeserializer
	auditMatcherProvider driver.AuditMatcherProvider
	recipientExtractor   driver.RecipientExtractor
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
	}
}

// GetOwnerVerifier returns the verifier associated to the passed owner identity.
func (d *Deserializer) GetOwnerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return d.ownerDeserializer.DeserializeVerifier(ctx, id)
}

// GetIssuerVerifier returns the verifier associated to the passed issuer identity.
func (d *Deserializer) GetIssuerVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return d.issuerDeserializer.DeserializeVerifier(ctx, id)
}

// GetAuditorVerifier returns the verifier associated to the passed auditor identity.
func (d *Deserializer) GetAuditorVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return d.auditorDeserializer.DeserializeVerifier(ctx, id)
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

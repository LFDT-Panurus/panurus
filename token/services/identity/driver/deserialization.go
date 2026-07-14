/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
)

// TypedSignerDeserializer converts a raw byte representation into a concrete
// Signer for a given IdentityType.
// Implementations should validate the raw data and return an error on invalid input or decoding failure.
//
//go:generate counterfeiter -o mock/tsdes.go -fake-name TypedSignerDeserializer . TypedSignerDeserializer
type TypedSignerDeserializer interface {
	// DeserializeSigner deserializes the provided raw bytes into a tdriver.Signer
	// appropriate for the supplied identity type.
	// The context may carry ancillary information required for deserialization.
	DeserializeSigner(ctx context.Context, typ IdentityType, raw []byte) (tdriver.Signer, error)
}

// TypedVerifierDeserializer deserializes verifiers and related identity information for typed identities.
// It provides methods to extract verifiers, recipients, audit information, and audit matchers
// from typed identity representations.
//
//go:generate counterfeiter -o mock/typed_verifier_deserializer.go -fake-name TypedVerifierDeserializer . TypedVerifierDeserializer
type TypedVerifierDeserializer interface {
	// DeserializeVerifier deserializes a verifier for the given identity type and raw identity
	DeserializeVerifier(ctx context.Context, typ IdentityType, raw []byte) (tdriver.Verifier, error)
	// Recipients returns the list of recipient identities from the given typed identity
	Recipients(id tdriver.Identity, typ IdentityType, raw []byte) ([]tdriver.Identity, error)
	// GetAuditInfo retrieves audit information for the given identity
	GetAuditInfo(ctx context.Context, id tdriver.Identity, typ IdentityType, raw []byte, p tdriver.AuditInfoProvider) ([]byte, error)
	// GetAuditInfoMatcher returns a matcher that can verify if an identity matches the given audit info
	GetAuditInfoMatcher(ctx context.Context, owner tdriver.Identity, auditInfo []byte) (tdriver.Matcher, error)
}

// AuditInfo represents the audit-related information for an identity.
// It exposes the enrollment id and the revocation handle necessary for audit
// and revocation operations.
//
//go:generate counterfeiter -o mock/audit_info.go -fake-name AuditInfo . AuditInfo
type AuditInfo interface {
	// EnrollmentID returns the enrollment identifier associated with this audit info.
	EnrollmentID() string
	// RevocationHandle returns the revocation handle associated with this audit info.
	RevocationHandle() string
}

// AuditInfoDeserializer converts a raw byte representation into an AuditInfo instance.
// Implementations should validate and parse the raw bytes and return an error on failure.
//
//go:generate counterfeiter -o mock/aides.go -fake-name AuditInfoDeserializer . AuditInfoDeserializer
type AuditInfoDeserializer interface {
	// DeserializeAuditInfo deserializes the provided raw bytes into an AuditInfo value.
	// The context may carry ancillary information required for deserialization.
	DeserializeAuditInfo(ctx context.Context, identity tdriver.Identity, raw []byte) (AuditInfo, error)
}

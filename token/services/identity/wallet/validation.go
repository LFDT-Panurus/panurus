/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wallet

import (
	"context"
	"encoding/json"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Validation errors
var (
	ErrEmptyIdentity           = errors.New("empty identity")
	ErrEmptyAuditInfo          = errors.New("empty audit info")
	ErrIdentityTooShort        = errors.New("identity too short")
	ErrIdentityTooLarge        = errors.New("identity too large")
	ErrEmptyRawIdentity        = errors.New("empty raw identity data")
	ErrAuditInfoTooLarge       = errors.New("audit info too large")
	ErrInvalidJSON             = errors.New("audit info is not valid JSON")
	ErrEmptyEnrollmentID       = errors.New("empty enrollment ID")
	ErrEnrollmentIDTooLong     = errors.New("enrollment ID too long")
	ErrInvalidEnrollmentIDChar = errors.New("invalid character in enrollment ID")
	ErrEmptyRevocationHandle   = errors.New("empty revocation handle")
	ErrRevocationHandleTooLong = errors.New("revocation handle too long")
)

const (
	// MaxEnrollmentIDLength defines the maximum allowed length for enrollment IDs
	MaxEnrollmentIDLength = 256
	// MaxRevocationHandleLength defines the maximum allowed length for revocation handles
	MaxRevocationHandleLength = 512
	// MinIdentityLength defines the minimum allowed length for identity data
	MinIdentityLength = 10
	// MaxIdentityLength defines the maximum allowed length for identity data (prevents DoS)
	MaxIdentityLength = 10000
	// MaxAuditInfoLength defines the maximum allowed length for audit info (prevents DoS)
	MaxAuditInfoLength = 50000
)

// validateBasicStructure performs nil and empty checks on RecipientData
func validateBasicStructure(data *tdriver.RecipientData) error {
	if data == nil {
		return ErrNilRecipientData
	}
	if len(data.Identity) == 0 {
		return ErrEmptyIdentity
	}
	if len(data.Identity) < MinIdentityLength {
		return errors.Wrapf(ErrIdentityTooShort, "identity is %d bytes (min %d)", len(data.Identity), MinIdentityLength)
	}
	if len(data.Identity) > MaxIdentityLength {
		return errors.Wrapf(ErrIdentityTooLarge, "identity is %d bytes (max %d)", len(data.Identity), MaxIdentityLength)
	}
	if len(data.AuditInfo) == 0 {
		return ErrEmptyAuditInfo
	}
	if len(data.AuditInfo) > MaxAuditInfoLength {
		return errors.Wrapf(ErrAuditInfoTooLarge, "audit info is %d bytes (max %d)", len(data.AuditInfo), MaxAuditInfoLength)
	}

	return nil
}

// validateJSONStructure validates that audit info is valid JSON
func validateJSONStructure(auditInfo []byte) error {
	if !json.Valid(auditInfo) {
		return ErrInvalidJSON
	}

	return nil
}

// validateEnrollmentID validates the enrollment ID format
// Skips validation for composite identity types (MultiSig, HTLC, Policy) that don't have enrollment IDs
func (s *Service) validateEnrollmentID(ctx context.Context, data *tdriver.RecipientData) error {
	// Extract identity type first to determine if EID validation applies
	typedID, err := identity.UnmarshalTypedIdentity(data.Identity)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal typed identity")
	}

	// Skip enrollment ID validation for composite identity types
	if isCompositeIdentityType(typedID.Type) {
		return nil
	}

	// Extract enrollment ID
	eid, err := s.IdentityProvider.GetEnrollmentID(ctx, data.Identity, data.AuditInfo)
	if err != nil {
		return errors.Wrap(err, "failed to extract enrollment ID")
	}

	// Check not empty
	if len(eid) == 0 {
		return ErrEmptyEnrollmentID
	}

	// Check length
	if len(eid) > MaxEnrollmentIDLength {
		return errors.Wrapf(ErrEnrollmentIDTooLong, "enrollment ID is %d bytes (max %d)", len(eid), MaxEnrollmentIDLength)
	}

	// Check format: alphanumeric + allowed special chars (-, _, .)
	for _, c := range eid {
		if !isValidEIDChar(c) {
			return errors.Wrapf(ErrInvalidEnrollmentIDChar, "character '%c' not allowed", c)
		}
	}

	return nil
}

// validateRevocationHandle validates the revocation handle format
// Skips validation for composite identity types (MultiSig, HTLC, Policy) that don't have revocation handles
func (s *Service) validateRevocationHandle(ctx context.Context, data *tdriver.RecipientData) error {
	// Extract identity type first to determine if RH validation applies
	typedID, err := identity.UnmarshalTypedIdentity(data.Identity)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal typed identity")
	}

	// Skip revocation handle validation for composite identity types
	if isCompositeIdentityType(typedID.Type) {
		return nil
	}

	// Extract revocation handle
	rh, err := s.IdentityProvider.GetRevocationHandler(ctx, data.Identity, data.AuditInfo)
	if err != nil {
		return errors.Wrap(err, "failed to extract revocation handle")
	}

	// Check not empty
	if len(rh) == 0 {
		return ErrEmptyRevocationHandle
	}

	// Check reasonable length
	if len(rh) > MaxRevocationHandleLength {
		return errors.Wrapf(ErrRevocationHandleTooLong, "revocation handle is %d bytes (max %d)", len(rh), MaxRevocationHandleLength)
	}

	return nil
}

// validateAuditInfoStructure validates audit info structure based on identity type
func (s *Service) validateAuditInfoStructure(ctx context.Context, data *tdriver.RecipientData) error {
	// Extract identity type
	typedID, err := identity.UnmarshalTypedIdentity(data.Identity)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal typed identity")
	}

	// Validate the identity type is recognized
	if !isValidIdentityType(typedID.Type) {
		return errors.Errorf("unknown identity type: %d", typedID.Type)
	}

	// Validate raw identity data is not empty
	if len(typedID.Identity) == 0 {
		return ErrEmptyRawIdentity
	}

	return nil
}

// isValidIdentityType checks if the identity type is one of the recognized types
func isValidIdentityType(t tdriver.IdentityType) bool {
	switch t {
	case tdriver.IdemixIdentityType,
		tdriver.X509IdentityType,
		tdriver.IdemixNymIdentityType,
		tdriver.HTLCScriptIdentityType,
		tdriver.MultiSigIdentityType,
		tdriver.PolicyIdentityType:
		return true
	default:
		return false
	}
}

// isCompositeIdentityType checks if the identity type is a composite type
// Composite types (MultiSig, HTLC, Policy) don't have enrollment IDs or revocation handles
func isCompositeIdentityType(t tdriver.IdentityType) bool {
	switch t {
	case tdriver.MultiSigIdentityType,
		tdriver.HTLCScriptIdentityType,
		tdriver.PolicyIdentityType:
		return true
	default:
		return false
	}
}

// isValidEIDChar checks if a character is valid for enrollment ID
// Allowed: alphanumeric + hyphen + underscore + dot
func isValidEIDChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.'
}

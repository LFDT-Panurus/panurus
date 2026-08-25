/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wallet

import (
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Validation errors
var (
	ErrEmptyIdentity     = errors.New("empty identity")
	ErrEmptyAuditInfo    = errors.New("empty audit info")
	ErrIdentityTooShort  = errors.New("identity too short")
	ErrIdentityTooLarge  = errors.New("identity too large")
	ErrAuditInfoTooLarge = errors.New("audit info too large")
)

const (
	// MinIdentityLength defines the minimum allowed length for identity data
	MinIdentityLength = 10
	// MaxIdentityLength defines the maximum allowed length for identity data (prevents DoS).
	// Set generously so legitimate identities are never rejected: composite identities
	// (MultiSig, Policy) aggregate several inner identities and X509 chains / Idemix proofs
	// can run to several KB. The bound exists only to reject pathological multi-MB blobs.
	MaxIdentityLength = 1 << 20 // 1 MiB
	// MaxAuditInfoLength defines the maximum allowed length for audit info (prevents DoS)
	MaxAuditInfoLength = 50000
)

// validateBasicStructure performs nil, empty, and length-bound checks on RecipientData.
// These checks are technology-agnostic (they make no assumption about the identity's
// encoding or the audit info's format): they only reject nil/empty input and pathological
// blob sizes as a denial-of-service guard. The cryptographic binding between identity and
// audit info, and any driver-specific structural validation, are performed downstream by
// the Deserializer's MatchIdentity and the driver's own deserializers.
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

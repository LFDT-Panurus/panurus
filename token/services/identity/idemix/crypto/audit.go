/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"context"

	csp "github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/schema"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
)

// Schema represents the version identifier for the credential schema.
type Schema = string

// AuditInfo contains cryptographic audit data for an Idemix identity.
type AuditInfo struct {
	// Enrollment ID pseudonym audit data
	EidNymAuditData *csp.AttrNymAuditData
	// Revocation handle pseudonym audit data
	RhNymAuditData *csp.AttrNymAuditData
	// Credential attributes (e.g. EnrollmentID, RevocationHandle strings)
	Attributes [][]byte

	// Cryptographic service provider
	Csp csp.BCCSP `json:"-"`
	// Credential issuer's public key
	IssuerPublicKey csp.Key `json:"-"`
	// Schema-specific operations manager
	SchemaManager schema.Manager `json:"-"`
	// Credential schema version
	Schema string
}

// Bytes serializes the AuditInfo to JSON format.
func (a *AuditInfo) Bytes() ([]byte, error) {
	return json.Marshal(a)
}

// FromBytes deserializes the AuditInfo from JSON format.
func (a *AuditInfo) FromBytes(raw []byte) error {
	return json.Unmarshal(raw, a)
}

// EnrollmentID returns the enrollment ID from Attributes[2].
func (a *AuditInfo) EnrollmentID() string {
	return string(a.Attributes[2])
}

// RevocationHandle returns the revocation handle from Attributes[3].
func (a *AuditInfo) RevocationHandle() string {
	return string(a.Attributes[3])
}

// Match verifies the identity matches this audit info by checking EID and RH pseudonyms.
func (a *AuditInfo) Match(_ context.Context, id []byte) error {
	serialized := new(SerializedIdemixIdentity)
	err := proto.Unmarshal(id, serialized)
	if err != nil {
		return errors.Wrap(err, "could not deserialize a SerializedIdemixIdentity")
	}

	eidAuditOpts, err := a.SchemaManager.EidNymAuditOpts(a.Schema, a.Attributes)
	if err != nil {
		return errors.Wrap(err, "error while getting a RhNymAuditOpts")
	}
	eidAuditOpts.RNymEid = a.EidNymAuditData.Rand

	// Audit EID
	valid, err := a.Csp.Verify(
		a.IssuerPublicKey,
		serialized.Proof,
		nil,
		eidAuditOpts,
	)
	if err != nil {
		return errors.Wrap(err, "error while verifying the nym eid")
	}
	if !valid {
		return errors.New("invalid nym rh")
	}

	rhAuditOpts, err := a.SchemaManager.RhNymAuditOpts(a.Schema, a.Attributes)
	if err != nil {
		return errors.Wrap(err, "error while getting a RhNymAuditOpts")
	}
	rhAuditOpts.RNymRh = a.RhNymAuditData.Rand

	// Audit RH
	valid, err = a.Csp.Verify(
		a.IssuerPublicKey,
		serialized.Proof,
		nil,
		rhAuditOpts,
	)
	if err != nil {
		return errors.Wrap(err, "error while verifying the nym rh")
	}
	if !valid {
		return errors.New("invalid nym eid")
	}

	return nil
}

// MinAuditAttributes is the number of Attributes entries EnrollmentID and
// RevocationHandle unconditionally index into (Attributes[2] and
// Attributes[3] respectively).
const MinAuditAttributes = 4

// Validate checks the invariants EnrollmentID, RevocationHandle, and Match
// rely on without further nil/bounds checking of their own. Callers that
// embed AuditInfo (e.g. idemixnym's nym.AuditInfo) must call this after
// deserializing raw, attacker-controlled bytes into it.
func (a *AuditInfo) Validate() error {
	if len(a.Attributes) < MinAuditAttributes {
		return errors.Errorf("failed to unmarshal, expected at least %d attributes, got %d", MinAuditAttributes, len(a.Attributes))
	}
	if a.EidNymAuditData == nil {
		return errors.New("failed to unmarshal, no EID nym audit data found")
	}
	if a.RhNymAuditData == nil {
		return errors.New("failed to unmarshal, no RH nym audit data found")
	}

	return nil
}

// DeserializeAuditInfo deserializes the audit information from JSON.
func DeserializeAuditInfo(raw []byte) (*AuditInfo, error) {
	auditInfo := &AuditInfo{}
	err := auditInfo.FromBytes(raw)
	if err != nil {
		return nil, err
	}
	if err := auditInfo.Validate(); err != nil {
		return nil, err
	}

	return auditInfo, nil
}

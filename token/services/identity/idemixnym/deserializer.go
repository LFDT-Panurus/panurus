/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package idemixnym

import (
	"context"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/driver"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/nym"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type Deserializer struct {
	backend *idemix.Deserializer
}

// NewDeserializer returns a new deserializer for the idemix ExpectEidNymRhNym verification strategy
func NewDeserializer(
	backend *idemix.Deserializer,
) *Deserializer {
	return &Deserializer{
		backend: backend,
	}
}

// DeserializeVerifier deserializes a given raw id into a new psudonym signature verifier with id's PK
func (d *Deserializer) DeserializeVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return &nym.Verifier{
		NymEID: id,
		Backed: d.backend,
	}, nil
}

// DeserializeAuditInfo deserializes a given raw AuditInfo into an AuditInfo
func (d *Deserializer) DeserializeAuditInfo(ctx context.Context, identity driver.Identity, raw []byte) (driver2.AuditInfo, error) {
	return d.deserializeAuditInfo(raw)
}

// GetAuditInfoMatcher deserializes a given raw AuditInfo into a Matcher
func (d *Deserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return d.deserializeAuditInfo(auditInfo)
}

// MatchIdentity deserializes the provided audit information (ai) and verify it matches the given identity (id)
// by verifying the related ZK proofs
func (d *Deserializer) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	matcher, err := d.GetAuditInfoMatcher(ctx, id, ai)
	if err != nil {
		return errors.WithMessagef(err, "failed to deserialize audit info")
	}

	return matcher.Match(ctx, id)
}

// Info returns a string that includes the given identity.
// If AuditInfo is also provided then the id is cryptographically verified against it
// and the Enrollment ID (EID) is extracted from the audit info and is printed as well.
func (d *Deserializer) Info(ctx context.Context, id []byte, auditInfoRaw []byte) (string, error) {
	eid := ""
	if len(auditInfoRaw) != 0 {
		err := d.MatchIdentity(ctx, id, auditInfoRaw)
		if err != nil {
			return "", errors.WithMessagef(err, "failed to get audit info matcher")
		}
		ai, err := d.DeserializeAuditInfo(ctx, nil, auditInfoRaw)
		if err != nil {
			return "", errors.Wrapf(err, "failed to deserialize audit info")
		}
		eid = ai.EnrollmentID()
	}

	return fmt.Sprintf("Idemix: [%s][%s]", eid, driver.Identity(id).UniqueID()), nil
}

// String returns the Issuer-PK as a string
func (d *Deserializer) String() string {
	return fmt.Sprintf("IdemixNym on [%s]", d.backend)
}

func (d *Deserializer) deserializeAuditInfo(raw []byte) (*nym.AuditInfo, error) {
	ai, err := nym.DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing audit info")
	}
	ai.Csp = d.backend.Csp
	ai.IssuerPublicKey = d.backend.IssuerPublicKey
	ai.SchemaManager = d.backend.SchemaManager
	ai.Schema = d.backend.Schema

	return ai, nil
}

type AuditInfoDeserializer struct{}

// DeserializeAuditInfo deserializes a given raw AuditInfo into an AuditInfo
func (c *AuditInfoDeserializer) DeserializeAuditInfo(ctx context.Context, identity driver.Identity, raw []byte) (driver2.AuditInfo, error) {
	ai, err := nym.DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing audit info [%s]", string(raw))
	}

	return ai, nil
}

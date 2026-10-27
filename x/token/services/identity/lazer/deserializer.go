/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Deserializer verifies identities and signatures under an issuer public key.
// It is safe for concurrent use.
type Deserializer struct {
	Ipk []byte
	pv  *presVerifier
}

// NewDeserializer returns a deserializer for the passed issuer public key.
func NewDeserializer(ipk []byte) (*Deserializer, error) {
	if len(ipk) == 0 {
		return nil, errors.New("invalid ipk")
	}
	pv, err := newPresVerifier(ipk)
	if err != nil {
		return nil, err
	}

	return &Deserializer{Ipk: append([]byte(nil), ipk...), pv: pv}, nil
}

// Close releases the C state of the deserializer.
func (d *Deserializer) Close() { d.pv.Close() }

// DeserializeVerifier returns the verifier of a pseudonym. The identity is
// only checked syntactically here; signatures carry the proof that the
// pseudonym is bound to a credential of the issuer.
func (d *Deserializer) DeserializeVerifier(_ context.Context, raw driver.Identity) (driver.Verifier, error) {
	id, err := UnmarshalIdentity(raw)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to deserialize identity")
	}

	return &Verifier{id: id, pv: d.pv}, nil
}

// DeserializeAuditInfo deserializes a raw audit info.
func (d *Deserializer) DeserializeAuditInfo(_ context.Context, _ driver.Identity, raw []byte) (idriver.AuditInfo, error) {
	return DeserializeAuditInfo(raw)
}

// GetAuditInfoMatcher deserializes a raw audit info into a matcher.
func (d *Deserializer) GetAuditInfoMatcher(_ context.Context, _ driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return DeserializeAuditInfo(auditInfo)
}

// MatchIdentity verifies that the audit info matches the identity.
func (d *Deserializer) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	matcher, err := DeserializeAuditInfo(ai)
	if err != nil {
		return errors.WithMessagef(err, "failed to deserialize audit info")
	}

	return matcher.Match(ctx, id)
}

// Info returns a string that includes the given identity. If audit info is
// also provided, it is verified against the identity and the enrollment id is
// printed as well.
func (d *Deserializer) Info(ctx context.Context, id []byte, auditInfoRaw []byte) (string, error) {
	eid := ""
	if len(auditInfoRaw) != 0 {
		ai, err := DeserializeAuditInfo(auditInfoRaw)
		if err != nil {
			return "", errors.WithMessagef(err, "failed to deserialize audit info")
		}
		if err := ai.Match(ctx, id); err != nil {
			return "", errors.WithMessagef(err, "failed to match audit info")
		}
		eid = ai.EnrollmentID()
	}

	return fmt.Sprintf("Lazer: [%s][%s]", eid, driver.Identity(id).UniqueID()), nil
}

// String returns the issuer public key as a string.
func (d *Deserializer) String() string {
	return fmt.Sprintf("Lazer with IPK [%s]", utils.Hashable(d.Ipk).String())
}

// AuditInfoDeserializer deserializes audit infos without an issuer public key.
type AuditInfoDeserializer struct{}

// DeserializeAuditInfo deserializes a given raw AuditInfo.
func (c *AuditInfoDeserializer) DeserializeAuditInfo(_ context.Context, _ driver.Identity, raw []byte) (idriver.AuditInfo, error) {
	ai, err := DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing audit info [%s]", utils.Hashable(raw))
	}

	return ai, nil
}

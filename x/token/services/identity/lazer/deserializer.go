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

// matcher deserializes a raw audit info into a matcher bound to the issuer.
func (d *Deserializer) matcher(raw []byte) (*auditMatcher, error) {
	ai, err := DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to deserialize audit info")
	}

	return &auditMatcher{ai: ai, pv: d.pv}, nil
}

// DeserializeAuditInfo deserializes a raw audit info and checks it against the
// identity id, as MatchIdentity does: the identity is required.
func (d *Deserializer) DeserializeAuditInfo(ctx context.Context, id driver.Identity, raw []byte) (idriver.AuditInfo, error) {
	if len(id) == 0 {
		return nil, errors.New("an identity is required to check the audit info")
	}
	m, err := d.matcher(raw)
	if err != nil {
		return nil, err
	}
	if err := m.Match(ctx, id); err != nil {
		return nil, errors.WithMessagef(err, "audit info does not match the identity")
	}

	return m.ai, nil
}

// GetAuditInfoMatcher deserializes a raw audit info into a matcher. The
// matcher checks the openings of the audit info and its presentation, under
// the issuer public key of the deserializer, against the identity to match.
func (d *Deserializer) GetAuditInfoMatcher(_ context.Context, _ driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	return d.matcher(auditInfo)
}

// MatchIdentity verifies that the audit info matches the identity: the nyms
// commit to its enrollment id and revocation handle, and its presentation
// binds them to a credential of the issuer.
func (d *Deserializer) MatchIdentity(ctx context.Context, id driver.Identity, ai []byte) error {
	m, err := d.matcher(ai)
	if err != nil {
		return err
	}

	return m.Match(ctx, id)
}

// Info returns a string that includes the given identity. If audit info is
// also provided, it is verified against the identity, as MatchIdentity does,
// and the enrollment id is printed as well.
func (d *Deserializer) Info(ctx context.Context, id []byte, auditInfoRaw []byte) (string, error) {
	eid := ""
	if len(auditInfoRaw) != 0 {
		m, err := d.matcher(auditInfoRaw)
		if err != nil {
			return "", err
		}
		if err := m.Match(ctx, id); err != nil {
			return "", errors.WithMessagef(err, "failed to match audit info")
		}
		eid = m.ai.EnrollmentID()
	}

	return fmt.Sprintf("Lazer: [%s][%s]", eid, driver.Identity(id).UniqueID()), nil
}

// String returns the issuer public key as a string.
func (d *Deserializer) String() string {
	return fmt.Sprintf("Lazer with IPK [%s]", utils.Hashable(d.Ipk).String())
}

// AuditInfoDeserializer deserializes audit infos without an issuer public key.
//
// Its scope is limited to reading the enrollment id and the revocation handle
// out of an audit info (as the EID/RH deserializer of the auditor and of the
// wallet manager does). Without the issuer public key it cannot check the
// presentation, so it never establishes that an identity has a credential:
// match identities with Deserializer.MatchIdentity first. It returns an
// *AuditInfo, which is not a driver.Matcher.
type AuditInfoDeserializer struct{}

// DeserializeAuditInfo deserializes a given raw AuditInfo. The identity is
// ignored, as by the Idemix AuditInfoDeserializer: nothing is matched here.
func (c *AuditInfoDeserializer) DeserializeAuditInfo(_ context.Context, _ driver.Identity, raw []byte) (idriver.AuditInfo, error) {
	ai, err := DeserializeAuditInfo(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed deserializing audit info [%s]", utils.Hashable(raw))
	}

	return ai, nil
}

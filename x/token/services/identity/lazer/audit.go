/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// maxAuditInfoLen bounds the size of an audit info before parsing it: the
// base64 encoded presentation alone takes about 60 KB.
const maxAuditInfoLen = 1 << 17

// AuditInfo lets an auditor learn the enrollment id and the revocation handle
// behind a pseudonym: it holds their values and the openings of the nyms, and
// the presentation that binds the nyms of the pseudonym to a credential.
//
// The openings reveal the committed values, by design: whoever holds the audit
// info learns the EID and RH. An opening of nymSK would likewise reveal usk,
// which is why it is never part of the audit info.
//
// AuditInfo has no Match method on purpose: matching an identity needs the
// issuer public key to check the presentation, without it a pseudonym made
// up from the victim's EID and RH, with no credential, would match. Use
// Deserializer.MatchIdentity or Deserializer.GetAuditInfoMatcher.
type AuditInfo struct {
	EID        string `json:"eid"`
	RH         string `json:"rh"`
	OpeningEID []byte `json:"opening_eid"`
	OpeningRH  []byte `json:"opening_rh"`
	// Pres is the presentation generated with the pseudonym (the one its signatures carry)
	Pres []byte `json:"pres"`
}

// Bytes serializes the audit info.
func (a *AuditInfo) Bytes() ([]byte, error) {
	return json.Marshal(a)
}

// EnrollmentID returns the enrollment id.
func (a *AuditInfo) EnrollmentID() string { return a.EID }

// RevocationHandle returns the revocation handle.
func (a *AuditInfo) RevocationHandle() string { return a.RH }

// DeserializeAuditInfo parses an audit info strictly: the input must be the
// encoding Bytes produces (no unknown, duplicate or case-variant members, no
// trailing data), the openings must have the length of an opening and the
// presentation must be non-empty and at most anoncreds.MaxLenPres() bytes.
// It does not check the audit info against any identity.
func DeserializeAuditInfo(raw []byte) (*AuditInfo, error) {
	if len(raw) == 0 || len(raw) > maxAuditInfoLen {
		return nil, errors.Errorf("invalid audit info length [%d]", len(raw))
	}
	a := &AuditInfo{}
	if err := canonicalUnmarshal(raw, a); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal audit info")
	}
	if len(a.OpeningEID) != anoncreds.MaxLenOpening() || len(a.OpeningRH) != anoncreds.MaxLenOpening() {
		return nil, errors.New("invalid opening length")
	}
	if len(a.Pres) == 0 || len(a.Pres) > anoncreds.MaxLenPres() {
		return nil, errors.Errorf("invalid presentation length [%d]", len(a.Pres))
	}

	return a, nil
}

// matchOpenings checks that the nyms of identity commit to the EID and RH of
// the audit info. It does not check that identity has a credential.
func (a *AuditInfo) matchOpenings(identity *SerializedIdentity) error {
	if !anoncreds.OpeningVerify(identity.NymEID, hashAttr(IdxEID, a.EID), a.OpeningEID) {
		return errors.New("enrollment id does not match the identity")
	}
	if !anoncreds.OpeningVerify(identity.NymRH, hashAttr(IdxRH, a.RH), a.OpeningRH) {
		return errors.New("revocation handle does not match the identity")
	}

	return nil
}

// auditMatcher matches identities against an audit info under an issuer
// public key.
type auditMatcher struct {
	ai *AuditInfo
	pv *presVerifier
}

var _ driver.Matcher = (*auditMatcher)(nil)

// Match checks that the nyms of the identity id commit to the EID and RH of
// the audit info, and that the presentation of the audit info proves that
// they are bound to a credential of the issuer.
func (m *auditMatcher) Match(_ context.Context, id []byte) error {
	identity, err := UnmarshalIdentity(id)
	if err != nil {
		return errors.Wrap(err, "failed to deserialize identity")
	}
	if err := m.ai.matchOpenings(identity); err != nil {
		return err
	}
	if err := m.pv.verifyPres(identity, m.ai.Pres); err != nil {
		return errors.Wrap(err, "the identity is not bound to a credential of the issuer")
	}

	return nil
}

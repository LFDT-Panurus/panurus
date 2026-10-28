/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// maxAuditInfoLen bounds the size of an audit info before parsing it: the
// base64 encoded presentation and proof take about 70 KB.
const maxAuditInfoLen = 1 << 17

// AuditInfo lets an auditor learn the enrollment id and the revocation handle
// behind a pseudonym nymEID, and check them.
//
// The opening of nymEID is not part of it in the clear: nymEID is the identity,
// and whoever holds its opening and any credential with the same enrollment id
// (the issuer can make one) could sign under it. Instead, EIDProof proves that
// nymEID commits to the enrollment id, revealing nothing else. EncOpeningEID is
// the opening encrypted under a key of the wallet, so that the wallet can
// derive new pseudonyms with the same nymEID from an audit info (see
// KeyManager.Identity); nobody else can use it, and Match ignores it.
//
// The opening of nymRH is in the clear: whoever holds the audit info learns the
// RH, by design. nymRH is not a signing key. An opening of nymSK would reveal
// usk, it is never part of the audit info.
//
// AuditInfo has no Match method on purpose: matching an identity needs the
// issuer public key to check the proofs, without it a pseudonym made up from
// the victim's EID and RH, with no credential, would match. Use
// Deserializer.MatchIdentity or Deserializer.GetAuditInfoMatcher.
type AuditInfo struct {
	EID string `json:"eid"`
	RH  string `json:"rh"`
	// NymEID is the identity the audit info is for
	NymEID []byte `json:"nym_eid"`
	// NymSK and NymRH are the other nyms of the presentation
	NymSK []byte `json:"nym_sk"`
	NymRH []byte `json:"nym_rh"`
	// OpeningRH opens NymRH to the revocation handle
	OpeningRH []byte `json:"opening_rh"`
	// EIDProof proves that NymEID commits to the enrollment id
	EIDProof []byte `json:"eid_proof"`
	// EncOpeningEID is nonce || AES-256-GCM(opening of NymEID), for the wallet only
	EncOpeningEID []byte `json:"enc_opening_eid"`
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
// trailing data), and nyms, openings and proofs must have valid lengths.
// It does not check the audit info against any identity.
func DeserializeAuditInfo(raw []byte) (*AuditInfo, error) {
	if len(raw) == 0 || len(raw) > maxAuditInfoLen {
		return nil, errors.Errorf("invalid audit info length [%d]", len(raw))
	}
	a := &AuditInfo{}
	if err := canonicalUnmarshal(raw, a); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal audit info")
	}
	if len(a.NymEID) != maxNymLen || len(a.NymSK) != maxNymLen || len(a.NymRH) != maxNymLen {
		return nil, errors.New("invalid nym length")
	}
	if len(a.OpeningRH) != anoncreds.MaxLenOpening() {
		return nil, errors.New("invalid opening length")
	}
	if len(a.EncOpeningEID) != gcmNonceLen+anoncreds.MaxLenOpening()+gcmTagLen {
		return nil, errors.New("invalid encrypted opening length")
	}
	if len(a.EIDProof) == 0 || len(a.EIDProof) > anoncreds.MaxLenSok() {
		return nil, errors.Errorf("invalid enrollment id proof length [%d]", len(a.EIDProof))
	}
	if len(a.Pres) == 0 || len(a.Pres) > anoncreds.MaxLenPres() {
		return nil, errors.Errorf("invalid presentation length [%d]", len(a.Pres))
	}

	return a, nil
}

// auditOpeningAD is the additional data of EncOpeningEID: it binds the
// encrypted opening to the identity and to the enrollment id and revocation
// handle of the audit info.
func (a *AuditInfo) auditOpeningAD() []byte {
	ad := append([]byte(auditOpeningDomain), a.NymEID...)
	ad = append(ad, hashAttr(IdxEID, a.EID)...)

	return append(ad, hashAttr(IdxRH, a.RH)...)
}

// auditMatcher matches identities against an audit info under an issuer
// public key.
type auditMatcher struct {
	ai *AuditInfo
	pv *presVerifier
}

var _ driver.Matcher = (*auditMatcher)(nil)

// Match checks that the identity id is the nymEID of the audit info and
// commits to its enrollment id, that nymRH commits to its revocation handle,
// and that the presentation binds the nyms to a credential of the issuer.
func (m *auditMatcher) Match(_ context.Context, id []byte) error {
	if err := checkIdentity(id); err != nil {
		return errors.Wrap(err, "failed to deserialize identity")
	}
	if !bytes.Equal(id, m.ai.NymEID) {
		return errors.New("the audit info is for another identity")
	}
	if err := m.pv.verifyEID(id, m.ai.EID, m.ai.EIDProof); err != nil {
		return errors.Wrap(err, "enrollment id does not match the identity")
	}
	if !anoncreds.OpeningVerify(m.ai.NymRH, hashAttr(IdxRH, m.ai.RH), m.ai.OpeningRH) {
		return errors.New("revocation handle does not match the identity")
	}
	if err := m.pv.verifyPres(m.ai.NymSK, id, m.ai.NymRH, m.ai.Pres); err != nil {
		return errors.Wrap(err, "the identity is not bound to a credential of the issuer")
	}

	return nil
}

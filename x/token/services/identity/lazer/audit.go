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

const maxAuditInfoLen = 1 << 16

// AuditInfo lets an auditor learn the enrollment id and the revocation handle
// behind a pseudonym: it holds their values and the openings of the nyms.
type AuditInfo struct {
	EID        string `json:"eid"`
	RH         string `json:"rh"`
	OpeningEID []byte `json:"opening_eid"`
	OpeningRH  []byte `json:"opening_rh"`
}

var (
	_ driver.Matcher = (*AuditInfo)(nil)
)

// Bytes serializes the audit info.
func (a *AuditInfo) Bytes() ([]byte, error) {
	return json.Marshal(a)
}

// EnrollmentID returns the enrollment id.
func (a *AuditInfo) EnrollmentID() string { return a.EID }

// RevocationHandle returns the revocation handle.
func (a *AuditInfo) RevocationHandle() string { return a.RH }

// DeserializeAuditInfo parses an audit info strictly.
func DeserializeAuditInfo(raw []byte) (*AuditInfo, error) {
	if len(raw) == 0 || len(raw) > maxAuditInfoLen {
		return nil, errors.Errorf("invalid audit info length [%d]", len(raw))
	}
	a := &AuditInfo{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(a); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal audit info")
	}
	if dec.More() {
		return nil, errors.New("trailing data after audit info")
	}
	if len(a.OpeningEID) != len(openingTmpl) || len(a.OpeningRH) != len(openingTmpl) {
		return nil, errors.New("invalid opening length")
	}

	return a, nil
}

// openingTmpl has the length of an opening.
var openingTmpl = make([]byte, anoncreds.MaxLenOpening())

// Match checks that the nyms of the identity id commit to the EID and RH of the audit info.
func (a *AuditInfo) Match(_ context.Context, id []byte) error {
	identity, err := UnmarshalIdentity(id)
	if err != nil {
		return errors.Wrap(err, "failed to deserialize identity")
	}
	if !anoncreds.OpeningVerify(identity.NymEID, hashAttr(IdxEID, a.EID), a.OpeningEID) {
		return errors.New("enrollment id does not match the identity")
	}
	if !anoncreds.OpeningVerify(identity.NymRH, hashAttr(IdxRH, a.RH), a.OpeningRH) {
		return errors.New("revocation handle does not match the identity")
	}

	return nil
}

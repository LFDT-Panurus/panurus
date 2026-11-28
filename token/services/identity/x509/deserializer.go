/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package x509

import (
	"bytes"
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// IdentityDeserializer takes an identity and returns an ECDSA verifier
type IdentityDeserializer struct{}

func (d *IdentityDeserializer) DeserializeVerifier(ctx context.Context, id driver.Identity) (driver.Verifier, error) {
	return crypto.DeserializeVerifier(id)
}

type AuditMatcherDeserializer struct{}

func (a *AuditMatcherDeserializer) GetAuditInfoMatcher(ctx context.Context, owner driver.Identity, auditInfo []byte) (driver.Matcher, error) {
	ai := &AuditInfo{}
	err := ai.FromBytes(auditInfo)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal")
	}

	return &AuditInfoMatcher{EnrollmentID: ai.EID, RevocationHandle: ai.RH}, nil
}

// AuditInfoMatcher matches an x509 identity against the enrollment ID and
// revocation handle of its audit info.
type AuditInfoMatcher struct {
	EnrollmentID     string
	RevocationHandle []byte
}

func (a *AuditInfoMatcher) Match(_ context.Context, id []byte) error {
	eid, err := crypto.GetEnrollmentID(id)
	if err != nil {
		return errors.Wrap(err, "failed to get enrollment ID")
	}
	if eid != a.EnrollmentID {
		return errors.Errorf("expected [%s], got [%s]", a.EnrollmentID, eid)
	}
	rh, err := crypto.GetRevocationHandle(id)
	if err != nil {
		return errors.Wrap(err, "failed to get revocation handle")
	}
	if !bytes.Equal(rh, a.RevocationHandle) {
		return errors.Errorf("revocation handle mismatch for enrollment ID [%s]", eid)
	}

	return nil
}

type AuditInfoDeserializer struct{}

func (a *AuditInfoDeserializer) DeserializeAuditInfo(ctx context.Context, identity driver.Identity, raw []byte) (idriver.AuditInfo, error) {
	ai := &AuditInfo{}
	err := ai.FromBytes(raw)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal")
	}
	logger.DebugfContext(ctx, "audit info [%s]", string(raw))

	return ai, nil
}

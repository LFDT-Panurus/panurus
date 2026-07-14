/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deserializer

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	driver2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// EIDRHDeserializer returns enrollment IDs behind the owners of token
type EIDRHDeserializer struct {
	deserializers map[identity.Type]driver2.AuditInfoDeserializer
}

// NewEIDRHDeserializer returns an enrollmentService
func NewEIDRHDeserializer() *EIDRHDeserializer {
	return &EIDRHDeserializer{
		deserializers: map[identity.Type]driver2.AuditInfoDeserializer{},
	}
}

func (e *EIDRHDeserializer) AddDeserializer(typ identity.Type, d driver2.AuditInfoDeserializer) {
	e.deserializers[typ] = d
}

// GetEnrollmentID returns the enrollmentID associated with the identity matched to the passed auditInfo
func (e *EIDRHDeserializer) GetEnrollmentID(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	ai, err := e.DeserializeAuditInfo(ctx, identity, auditInfo)
	if err != nil {
		return "", err
	}

	return ai.EnrollmentID(), nil
}

// GetRevocationHandler returns the revocation handle associated with the identity matched to the passed auditInfo
func (e *EIDRHDeserializer) GetRevocationHandler(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	ai, err := e.DeserializeAuditInfo(ctx, identity, auditInfo)
	if err != nil {
		return "", err
	}

	return ai.RevocationHandle(), nil
}

func (e *EIDRHDeserializer) GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error) {
	ai, err := e.DeserializeAuditInfo(ctx, identity, auditInfo)
	if err != nil {
		return "", "", err
	}

	return ai.EnrollmentID(), ai.RevocationHandle(), nil
}

func (e *EIDRHDeserializer) DeserializeAuditInfo(ctx context.Context, id driver.Identity, auditInfo []byte) (driver2.AuditInfo, error) {
	if len(auditInfo) == 0 {
		return nil, errors.Errorf("nil audit info")
	}

	si, err := identity.UnmarshalTypedIdentity(id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal to TypedIdentity")
	}
	d, ok := e.deserializers[si.Type]
	if !ok {
		return nil, errors.Errorf("no deserializer found for [%v]", si.Type)
	}
	res, err := d.DeserializeAuditInfo(ctx, si.Identity, auditInfo)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to deserialize audit info for identity type [%v]", si.Type)
	}

	return res, nil
}

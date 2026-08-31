/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"context"

	"github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type AuditInfo struct {
	*crypto.AuditInfo
	IdemixSignature []byte
}

// FromBytes deserializes the AuditInfo from JSON format.
//
// The decode is strict (unknown fields rejected), matching how the embedded crypto.AuditInfo is
// parsed on every other entry point: the same logical type must not be laxer here just because it
// arrived through the nym wrapper.
func (a *AuditInfo) FromBytes(raw []byte) error {
	// The embedded crypto.AuditInfo's fields are inlined into this struct's JSON,
	// so this decode reaches the panicking mathlib curve elements itself rather
	// than going through crypto.AuditInfo.FromBytes. Guard it here too.
	if err := crypto.UnmarshalAuditInfo(func() error {
		return json.Unmarshal(raw, a)
	}); err != nil {
		return err
	}
	// The embedded pointer is only allocated if the payload carried at least one of the promoted
	// crypto.AuditInfo fields. Reject it here rather than leaving a nil embedded pointer for the
	// caller to dereference through a promoted method or field.
	if a.AuditInfo == nil {
		return errors.New("failed to unmarshal, no audit info found")
	}

	return nil
}

func (a *AuditInfo) Match(ctx context.Context, id []byte) error {
	if err := a.AuditInfo.Match(ctx, a.IdemixSignature); err != nil {
		return err
	}

	eidAuditOpts, err := a.SchemaManager.EidNymAuditOpts(a.Schema, a.Attributes)
	if err != nil {
		return errors.Wrap(err, "error while getting a RhNymAuditOpts")
	}
	eidAuditOpts.RNymEid = a.EidNymAuditData.Rand
	eidAuditOpts.AuditVerificationType = types.AuditExpectEidNym

	valid, err := a.Csp.Verify(
		a.IssuerPublicKey,
		id,
		nil,
		eidAuditOpts,
	)
	if err != nil {
		return errors.Wrap(err, "error while verifying the nym eid")
	}
	if !valid {
		return errors.New("invalid nym eid")
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
	// FromBytes already rejects a nil embedded AuditInfo.
	if err := auditInfo.Validate(); err != nil {
		return nil, err
	}
	if len(auditInfo.IdemixSignature) == 0 {
		return nil, errors.Errorf("failed to unmarshal, no idemix signature found")
	}

	return auditInfo, nil
}

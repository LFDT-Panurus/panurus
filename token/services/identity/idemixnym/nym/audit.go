/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nym

import (
	"context"
	"encoding/json"

	"github.com/IBM/idemix/bccsp/types"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type AuditInfo struct {
	*crypto.AuditInfo
	IdemixSignature []byte
}

// FromBytes deserializes the AuditInfo from JSON format.
func (a *AuditInfo) FromBytes(raw []byte) error {
	return json.Unmarshal(raw, a)
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
	if auditInfo.AuditInfo == nil {
		return nil, errors.Errorf("failed to unmarshal, no audit info found")
	}
	if len(auditInfo.Attributes) == 0 {
		return nil, errors.Errorf("failed to unmarshal, no attributes found")
	}
	if len(auditInfo.IdemixSignature) == 0 {
		return nil, errors.Errorf("failed to unmarshal, no idemix signature found")
	}

	return auditInfo, nil
}

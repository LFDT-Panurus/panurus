/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	protosv1 "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/token"
)

// ToTokenID converts *driver.TokenID to *token.ID
func ToTokenID(id *protosv1.TokenID) *token.ID {
	if id == nil {
		return nil
	}

	return &token.ID{
		TxId:  id.TxId,
		Index: id.Index,
	}
}

// ToProtoIdentitySlice converts []Identity to []*driver.Identity
func ToProtoIdentitySlice(identities []Identity) []*protosv1.Identity {
	res := make([]*protosv1.Identity, len(identities))
	for i, id := range identities {
		res[i] = &protosv1.Identity{
			Raw: id,
		}
	}

	return res
}

// FromProtoIdentitySlice converts []*driver.Identity to []Identity
func FromProtoIdentitySlice(identities []*protosv1.Identity) []Identity {
	res := make([]Identity, len(identities))
	for i, id := range identities {
		if id != nil {
			res[i] = id.Raw
		}
	}

	return res
}

// ToIdentity converts *driver.Identity to Identity
func ToIdentity(id *protosv1.Identity) Identity {
	if id == nil {
		return nil
	}

	return id.Raw
}

// ToProtoAuditableIdentitySlice converts []Identity to []*request.AuditableIdentity
func ToProtoAuditableIdentitySlice(identities []Identity) []*request.AuditableIdentity {
	res := make([]*request.AuditableIdentity, len(identities))
	for i, id := range identities {
		res[i] = &request.AuditableIdentity{
			Identity: &protosv1.Identity{
				Raw: id,
			},
			AuditInfo: nil, // No audit info for plain identities
		}
	}

	return res
}

// FromProtoAuditableIdentitySlice converts []*request.AuditableIdentity to []Identity
func FromProtoAuditableIdentitySlice(identities []*request.AuditableIdentity) []Identity {
	res := make([]Identity, len(identities))
	for i, id := range identities {
		if id != nil && id.Identity != nil {
			res[i] = id.Identity.Raw
		}
	}

	return res
}

// ToProtoAuditableIdentitySliceFromAuditable converts []AuditableIdentity to []*request.AuditableIdentity
func ToProtoAuditableIdentitySliceFromAuditable(identities []AuditableIdentity) ([]*request.AuditableIdentity, error) {
	res := make([]*request.AuditableIdentity, len(identities))
	for i, id := range identities {
		proto, err := id.ToProtos()
		if err != nil {
			return nil, err
		}
		res[i] = proto
	}

	return res, nil
}

// FromProtoAuditableIdentitySliceToAuditable converts []*request.AuditableIdentity to []AuditableIdentity
func FromProtoAuditableIdentitySliceToAuditable(identities []*request.AuditableIdentity) ([]AuditableIdentity, error) {
	res := make([]AuditableIdentity, len(identities))
	for i, id := range identities {
		if err := res[i].FromProtos(id); err != nil {
			return nil, err
		}
	}

	return res, nil
}

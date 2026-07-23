/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package guard

import (
	"context"

	tokendriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	driver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
)

// WrapIdentity wraps an identity store with the guard policy.
func WrapIdentity(s driver.IdentityStore, p Policy) driver.IdentityStore {
	if s == nil {
		return s
	}

	return &guardedIdentityStore{IdentityStore: s, policy: p}
}

type guardedIdentityStore struct {
	driver.IdentityStore
	policy Policy
}

// IteratorConfigurations is intentionally not row-capped and delegates to the
// embedded store unchanged. It takes no pagination argument, and
// LocalMembership.storedIdentityConfigurations drains it in full on the identity
// Load path, so a cap here would turn "this node has many registered identities"
// into a hard identity-loading failure with no way for the caller to page around
// it. Bounding this read needs a SQL-level LIMIT (tracked follow-up).

func (g *guardedIdentityStore) AddConfiguration(ctx context.Context, wp idriver.IdentityConfiguration) error {
	size := len(wp.ID) + len(wp.Type) + len(wp.URL) + len(wp.Config) + len(wp.Raw)
	if err := CheckPayload("AddConfiguration", size, g.policy.MaxPayloadSize); err != nil {
		return err
	}

	return g.IdentityStore.AddConfiguration(ctx, wp)
}

func (g *guardedIdentityStore) StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error {
	size := len(id) + len(identityAudit) + len(tokenMetadata) + len(tokenMetadataAudit)
	if err := CheckPayload("StoreIdentityData", size, g.policy.MaxPayloadSize); err != nil {
		return err
	}

	return g.IdentityStore.StoreIdentityData(ctx, id, identityAudit, tokenMetadata, tokenMetadataAudit)
}

func (g *guardedIdentityStore) StoreSignerInfo(ctx context.Context, id tokendriver.Identity, info []byte) error {
	if err := CheckPayload("StoreSignerInfo", len(id)+len(info), g.policy.MaxPayloadSize); err != nil {
		return err
	}

	return g.IdentityStore.StoreSignerInfo(ctx, id, info)
}

func (g *guardedIdentityStore) RegisterIdentityDescriptor(ctx context.Context, descriptor *idriver.IdentityDescriptor, alias tokendriver.Identity) error {
	size := len(alias)
	if descriptor != nil {
		size += len(descriptor.Identity) + len(descriptor.AuditInfo) + len(descriptor.SignerInfo)
	}
	if err := CheckPayload("RegisterIdentityDescriptor", size, g.policy.MaxPayloadSize); err != nil {
		return err
	}

	return g.IdentityStore.RegisterIdentityDescriptor(ctx, descriptor, alias)
}

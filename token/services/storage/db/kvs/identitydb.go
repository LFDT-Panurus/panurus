/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvs

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
)

const (
	IdentityDBPrefix              = "idb"
	IdentityDBConfigurationPrefix = "configuration"
	IdentityDBData                = "data"
	IdentityDBSigner              = "signer"
)

// RecipientData contains information about the identity of a token owner
type RecipientData struct {
	// AuditInfo contains private information Identity
	AuditInfo []byte
	// TokenMetadata contains public information related to the token to be assigned to this Recipient.
	TokenMetadata []byte
	// TokenMetadataAuditInfo contains private information TokenMetadata
	TokenMetadataAuditInfo []byte
}

type IdentityStore struct {
	kvs   KVS
	tmsID token.TMSID
}

func NewIdentityStore(kvs KVS, tmsID token.TMSID) *IdentityStore {
	return &IdentityStore{kvs: kvs, tmsID: tmsID}
}

func (s *IdentityStore) AddConfiguration(ctx context.Context, wp storage.IdentityConfiguration) error {
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			wp.Type,
			mergeIDURL(wp.ID, wp.URL),
		},
	)
	if err != nil {
		return errors.Wrapf(err, "failed to create key")
	}

	return s.kvs.Put(ctx, k, &wp)
}

func (s *IdentityStore) GetConfiguration(ctx context.Context, id, typ, url string) (*storage.IdentityConfiguration, error) {
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			typ,
			mergeIDURL(id, url),
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create key")
	}

	if !s.kvs.Exists(ctx, k) {
		return nil, nil
	}

	var res storage.IdentityConfiguration
	if err := s.kvs.Get(ctx, k, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (s *IdentityStore) IteratorConfigurations(ctx context.Context, configurationType string) (idriver.IdentityConfigurationIterator, error) {
	it, err := s.kvs.GetByPartialCompositeID(
		ctx,
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			configurationType,
		},
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get registered identities from kvs")
	}

	return &IdentityConfigurationsIterator{Iterator: it}, nil
}

func (s *IdentityStore) ConfigurationExists(ctx context.Context, id, configurationType, url string) (bool, error) {
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBConfigurationPrefix,
			s.tmsID.String(),
			configurationType,
			mergeIDURL(id, url),
		},
	)
	if err != nil {
		return false, errors.Wrapf(err, "failed to create key")
	}

	return s.kvs.Exists(ctx, k), nil
}

func (s *IdentityStore) Notifier() (idriver.IdentityConfigurationNotifier, error) {
	return nil, storage.ErrNotSupported
}

func (s *IdentityStore) StoreIdentityData(ctx context.Context, id []byte, identityAudit []byte, tokenMetadata []byte, tokenMetadataAudit []byte) error {
	k := kvs.CreateCompositeKeyOrPanic(
		IdentityDBPrefix,
		[]string{
			IdentityDBData,
			s.tmsID.String(),
			tdriver.Identity(id).String(),
		},
	)
	if err := s.kvs.Put(ctx, k, &RecipientData{
		AuditInfo:              identityAudit,
		TokenMetadata:          tokenMetadata,
		TokenMetadataAuditInfo: tokenMetadataAudit,
	}); err != nil {
		return err
	}

	return nil
}

func (s *IdentityStore) GetAuditInfo(ctx context.Context, identity []byte) ([]byte, error) {
	k := kvs.CreateCompositeKeyOrPanic(
		IdentityDBPrefix,
		[]string{
			IdentityDBData,
			s.tmsID.String(),
			tdriver.Identity(identity).String(),
		},
	)
	if !s.kvs.Exists(ctx, k) {
		return nil, nil
	}
	var res RecipientData
	if err := s.kvs.Get(ctx, k, &res); err != nil {
		return nil, err
	}

	return res.AuditInfo, nil
}

func (s *IdentityStore) GetTokenInfo(ctx context.Context, identity []byte) ([]byte, []byte, error) {
	k := kvs.CreateCompositeKeyOrPanic(
		IdentityDBPrefix,
		[]string{
			IdentityDBData,
			s.tmsID.String(),
			tdriver.Identity(identity).String(),
		},
	)
	if !s.kvs.Exists(ctx, k) {
		return nil, nil, nil
	}
	var res RecipientData
	if err := s.kvs.Get(ctx, k, &res); err != nil {
		return nil, nil, err
	}

	return res.TokenMetadata, res.TokenMetadataAuditInfo, nil
}

func (s *IdentityStore) StoreSignerInfo(ctx context.Context, id tdriver.Identity, info []byte) error {
	idHash := id.UniqueID()
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBSigner,
			s.tmsID.String(),
			idHash,
		},
	)
	if err != nil {
		return errors.Wrap(err, "failed to create composite key to store entry in kvs")
	}
	err = s.kvs.Put(ctx, k, info)
	if err != nil {
		return errors.Wrap(err, "failed to store entry in kvs for the passed signer")
	}

	return nil
}

func (s *IdentityStore) GetExistingSignerInfo(ctx context.Context, identities ...tdriver.Identity) ([]string, error) {
	keys := make([]string, len(identities))
	for i, id := range identities {
		k, err := kvs.CreateCompositeKey(
			IdentityDBPrefix,
			[]string{
				IdentityDBSigner,
				s.tmsID.String(),
				id.UniqueID(),
			},
		)
		if err != nil {
			return nil, err
		}
		keys[i] = k
	}

	return s.kvs.GetExisting(ctx, keys...), nil
}

func (s *IdentityStore) SignerInfoExists(ctx context.Context, id []byte) (bool, error) {
	existing, err := s.GetExistingSignerInfo(ctx, id)
	if err != nil {
		return false, err
	}

	return len(existing) > 0, nil
}

func (s *IdentityStore) GetSignerInfo(ctx context.Context, identity []byte) ([]byte, error) {
	idHash := tdriver.Identity(identity).UniqueID()
	k, err := kvs.CreateCompositeKey(
		IdentityDBPrefix,
		[]string{
			IdentityDBSigner,
			s.tmsID.String(),
			idHash,
		},
	)
	if err != nil {
		return nil, err
	}
	var res []byte
	if err := s.kvs.Get(ctx, k, &res); err != nil {
		return nil, err
	}

	return res, nil
}

func (s *IdentityStore) RegisterIdentityDescriptor(ctx context.Context, descriptor *idriver.IdentityDescriptor, alias tdriver.Identity) error {
	if err := s.StoreSignerInfo(ctx, descriptor.Identity, descriptor.SignerInfo); err != nil {
		return err
	}
	if err := s.StoreSignerInfo(ctx, alias, descriptor.SignerInfo); err != nil {
		return err
	}
	if err := s.StoreIdentityData(ctx, descriptor.Identity, descriptor.AuditInfo, nil, nil); err != nil {
		return err
	}
	if err := s.StoreIdentityData(ctx, alias, descriptor.AuditInfo, nil, nil); err != nil {
		return err
	}

	return nil
}

func (s *IdentityStore) Close() error {
	return nil
}

// IterateSigners is not supported by the KVS-backed identity store.
// It returns ErrNotSupported, consistent with other unsupported operations on this store.
func (s *IdentityStore) IterateSigners(_ context.Context, _, _ int) ([]idriver.SignerEntry, error) {
	return nil, storage.ErrNotSupported
}

type IdentityConfigurationsIterator struct {
	kvs.Iterator
}

func (w *IdentityConfigurationsIterator) Next() (*storage.IdentityConfiguration, error) {
	if !w.HasNext() {
		return nil, nil
	}
	idConfig := &storage.IdentityConfiguration{}
	_, err := w.Iterator.Next(idConfig)
	if err != nil {
		return nil, err
	}

	return idConfig, nil
}

func (w *IdentityConfigurationsIterator) Close() {
	_ = w.Iterator.Close()
}

func mergeIDURL(id string, url string) string {
	return base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s%s", id, url))
}

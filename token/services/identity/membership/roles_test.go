/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package membership_test

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRoleFactory(t *testing.T) {
	logger := logging.MustGetLogger("test")
	tmsID := token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}
	config := &mock.Config{}
	fscIdentity := driver.Identity("fscIdentity")
	networkDefaultIdentity := driver.Identity("networkDefaultIdentity")
	identityProvider := &mock.IdentityProvider{}
	storageProvider := &mock.StorageProvider{}
	deserializerManager := &mock.SignerDeserializerManager{}

	factory := membership.NewRoleFactory(
		logger,
		tmsID,
		config,
		fscIdentity,
		networkDefaultIdentity,
		identityProvider,
		storageProvider,
		deserializerManager,
	)

	assert.NotNil(t, factory)
	assert.Equal(t, logger, factory.Logger)
	assert.Equal(t, tmsID, factory.TMSID)
	assert.Equal(t, config, factory.Config)
	assert.Equal(t, fscIdentity, factory.FSCIdentity)
	assert.Equal(t, networkDefaultIdentity, factory.NetworkDefaultIdentity)
	assert.Equal(t, identityProvider, factory.IdentityProvider)
	assert.Equal(t, storageProvider, factory.StorageProvider)
	assert.Equal(t, deserializerManager, factory.DeserializerManager)
}

func TestRoleFactory_NewRole(t *testing.T) {
	logger := logging.MustGetLogger("test")
	tmsID := token.TMSID{Network: "network", Channel: "channel", Namespace: "namespace"}
	config := &mock.Config{}
	fscIdentity := driver.Identity("fscIdentity")
	networkDefaultIdentity := driver.Identity("networkDefaultIdentity")
	identityProvider := &mock.IdentityProvider{}
	storageProvider := &mock.StorageProvider{}
	deserializerManager := &mock.SignerDeserializerManager{}
	identityStore := &mock2.IdentityStoreService{}
	identityStore.NotifierReturns(nil, storage.ErrNotSupported)

	factory := membership.NewRoleFactory(
		logger,
		tmsID,
		config,
		fscIdentity,
		networkDefaultIdentity,
		identityProvider,
		storageProvider,
		deserializerManager,
	)

	t.Run("Success", func(t *testing.T) {
		storageProvider.IdentityStoreReturns(identityStore, nil)
		config.IdentitiesForRoleReturns([]idriver.ConfiguredIdentity{
			{ID: "id1", Path: "path1"},
		}, nil)
		identityStore.IteratorConfigurationsReturns(&mock.IdentityConfigurationIterator{}, nil)

		role, err := factory.NewRole(idriver.OwnerRole, true, nil)
		require.NoError(t, err)
		assert.NotNil(t, role)
		assert.Equal(t, idriver.OwnerRole, role.ID())
	})

	t.Run("StorageProviderError", func(t *testing.T) {
		storageProvider.IdentityStoreReturns(nil, errors.New("storage error"))

		role, err := factory.NewRole(idriver.OwnerRole, true, nil)
		require.Error(t, err)
		assert.Nil(t, role)
		assert.Contains(t, err.Error(), "failed to get wallet path storage")
	})

	t.Run("ConfigError", func(t *testing.T) {
		storageProvider.IdentityStoreReturns(identityStore, nil)
		config.IdentitiesForRoleReturns(nil, errors.New("config error"))

		role, err := factory.NewRole(idriver.OwnerRole, true, nil)
		require.Error(t, err)
		assert.Nil(t, role)
		assert.Contains(t, err.Error(), "failed to get identities for role")
	})

	t.Run("LoadError", func(t *testing.T) {
		storageProvider.IdentityStoreReturns(identityStore, nil)
		config.IdentitiesForRoleReturns([]idriver.ConfiguredIdentity{
			{ID: "id1", Path: "path1"},
		}, nil)
		// Simulate Load error by making IteratorConfigurations fail
		identityStore.IteratorConfigurationsReturns(nil, errors.New("iterator error"))

		role, err := factory.NewRole(idriver.OwnerRole, true, nil)
		require.Error(t, err)
		assert.Nil(t, role)
		assert.Contains(t, err.Error(), "failed to load identities")
	})
}

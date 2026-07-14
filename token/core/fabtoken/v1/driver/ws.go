/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	v2 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/config"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
)

type BaseWalletServiceFactory struct {
	PublicParametersDeserializer
}

// newWalletService returns a new wallet service for the passed configuration and parameters.
// newWalletService returns a new wallet service for the passed configuration and parameters.
func (d BaseWalletServiceFactory) newWalletService(
	tmsConfig core.Config,
	binder identity.NetworkBinderService,
	storageProvider identity.StorageProvider,
	qe driver.QueryEngine,
	logger logging.Logger,
	fscIdentity driver.Identity,
	networkDefaultIdentity driver.Identity,
	pp driver.PublicParameters,
	ignoreRemote bool,
) (*wallet.Service, error) {
	tmsID := tmsConfig.ID()

	deserializerManager := deserializer.NewTypedSignerDeserializerMultiplex()
	identityDB, err := storageProvider.IdentityStore(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open identity db for tms [%s]", tmsID)
	}
	baseKeyStore, err := storageProvider.Keystore(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open keystore for tms [%s]", tmsID)
	}
	identityProvider := identity.NewProvider(logger.Named("identity"), identityDB, deserializerManager, binder, NewEIDRHDeserializer())
	identityConfig, err := config.NewIdentityConfig(tmsConfig)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create identity config")
	}

	// Prepare roles
	keyStore := x509.NewKeyStore(baseKeyStore)
	roleFactory := membership.NewRoleFactory(
		logger,
		tmsID,
		identityConfig,
		fscIdentity,
		networkDefaultIdentity,
		identityProvider,
		storageProvider,
		deserializerManager,
	)
	newRole, err := roleFactory.NewRole(identity.OwnerRole, false, nil, x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create owner role")
	}
	roles := role.NewRoles()
	roles.Register(identity.OwnerRole, newRole)
	newRole, err = roleFactory.NewRole(identity.IssuerRole, false, pp.Issuers(), x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create issuer role")
	}
	roles.Register(identity.IssuerRole, newRole)
	newRole, err = roleFactory.NewRole(identity.AuditorRole, false, pp.Auditors(), x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create auditor role")
	}
	roles.Register(identity.AuditorRole, newRole)
	newRole, err = roleFactory.NewRole(identity.CertifierRole, false, nil, x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create certifier role")
	}
	roles.Register(identity.CertifierRole, newRole)

	// Instantiate the wallet service
	walletDB, err := storageProvider.WalletStore(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get identity storage provider")
	}
	deserializer := NewDeserializer()
	ws := wallet.NewService(
		logger,
		identityProvider,
		deserializer,
		wallet.Convert(roles.Registries(logger, walletDB, role.NewDefaultFactory(logger, identityProvider, qe, identityConfig, deserializer, &disabled.Provider{}))),
	)

	return ws, nil
}

// WalletServiceFactory is a factory for fabtoken wallet services.
type WalletServiceFactory struct {
	BaseWalletServiceFactory

	storageProvider identity.StorageProvider
}

// NewWalletServiceFactory returns a new factory for fabtoken wallet services.
func NewWalletServiceFactory(storageProvider identity.StorageProvider) core.NamedFactory[driver.WalletServiceFactory] {
	return core.NamedFactory[driver.WalletServiceFactory]{
		Name:   core.DriverIdentifier(v2.FabTokenDriverName, v2.ProtocolV1),
		Driver: &WalletServiceFactory{storageProvider: storageProvider},
	}
}

// NewWalletService returns a new fabtoken wallet service for the passed configuration and parameters.
func (d *WalletServiceFactory) NewWalletService(tmsConfig driver.Configuration, params driver.PublicParameters) (driver.WalletService, error) {
	tmsID := tmsConfig.ID()
	logger := logging.DriverLogger("panurus.driver.fabtoken", tmsID.Network, tmsID.Channel, tmsID.Namespace)

	return d.newWalletService(
		tmsConfig,
		&membership.NoBinder{},
		d.storageProvider,
		nil,
		logger,
		nil,
		nil,
		params,
		true,
	)
}

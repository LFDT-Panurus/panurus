/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/config"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	msp2 "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type BaseWalletServiceFactory struct {
	PublicParametersDeserializer
}

// NewWalletService returns a new zkatdlog wallet service.
func (d *BaseWalletServiceFactory) NewWalletService(
	tmsConfig core.Config,
	binder identity.NetworkBinderService,
	storageProvider identity.StorageProvider,
	qe driver.QueryEngine,
	logger logging.Logger,
	fscIdentity view.Identity,
	networkDefaultIdentity view.Identity,
	publicParams driver.PublicParameters,
	ignoreRemote bool,
	metricsProvider metrics.Provider,
) (*wallet.Service, error) {
	pp := publicParams.(*v1.PublicParams)
	roles := role.NewRoles()
	deserializerManager := deserializer.NewTypedSignerDeserializerMultiplex()
	tmsID := tmsConfig.ID()
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
	// owner role
	// we have one key manager for fabtoken and one for each idemix issuer public key
	kmps := make([]membership.KeyManagerProvider, 0, len(pp.IdemixIssuerPublicKeys)+1)
	for _, key := range pp.IdemixIssuerPublicKeys {
		keyStore, err := msp2.NewKeyStore(key.Curve, baseKeyStore)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to instantiate bccsp key store")
		}
		kmp := idemixnym.NewKeyManagerProvider(
			key.PublicKey,
			key.Curve,
			keyStore,
			identityConfig,
			identityConfig.DefaultCacheSize(),
			ignoreRemote,
			metricsProvider,
			identityDB,
		)
		kmps = append(kmps, kmp)
	}
	keyStore := x509.NewKeyStore(baseKeyStore)
	kmps = append(kmps, x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))

	newRole, err := roleFactory.NewRole(identity.OwnerRole, true, nil, kmps...)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create owner role")
	}
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

	// wallet service
	walletDB, err := storageProvider.WalletStore(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get identity storage provider")
	}
	deserializer, err := NewDeserializer(pp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to instantiate the deserializer")
	}

	return wallet.NewService(
		logger,
		identityProvider,
		deserializer,
		wallet.Convert(roles.Registries(logger, walletDB, role.NewDefaultFactory(logger, identityProvider, qe, identityConfig, deserializer, metricsProvider))),
	), nil
}

// WalletServiceFactory is a factory for creating zkatdlog wallet services.
type WalletServiceFactory struct {
	*BaseWalletServiceFactory

	storageProvider identity.StorageProvider
}

// NewWalletServiceFactory returns a new factory for the zkatdlog wallet service.
func NewWalletServiceFactory(storageProvider identity.StorageProvider) core.NamedFactory[driver.WalletServiceFactory] {
	return core.NamedFactory[driver.WalletServiceFactory]{
		Name: core.DriverIdentifier(v1.DLogNoGHDriverName, v1.ProtocolV1),
		Driver: &WalletServiceFactory{
			BaseWalletServiceFactory: &BaseWalletServiceFactory{},
			storageProvider:          storageProvider},
	}
}

// NewWalletService returns a new zkatdlog wallet service for the passed configuration and public parameters.
func (d *WalletServiceFactory) NewWalletService(tmsConfig driver.Configuration, params driver.PublicParameters) (driver.WalletService, error) {
	tmsID := tmsConfig.ID()
	logger := logging.DriverLogger("panurus.driver.zkatdlog", tmsID.Network, tmsID.Channel, tmsID.Namespace)

	pp, ok := params.(*v1.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", params)
	}

	return d.BaseWalletServiceFactory.NewWalletService(
		tmsConfig,
		&membership.NoBinder{},
		d.storageProvider,
		nil,
		logger,
		nil,
		nil,
		pp,
		true,
		&disabled.Provider{},
	)
}

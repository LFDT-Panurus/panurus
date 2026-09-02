/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
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
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type BaseWalletServiceFactory struct {
	PublicParametersDeserializer
}

// NewWalletService returns a new zkatsnark wallet service.
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
	ppp := publicParams.(*pp.PublicParams)
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
	identityMetrics := identity.NewMetrics(metricsProvider)
	signerRouter := identity.NewSignerRouter(identityMetrics)
	identityProvider := identity.NewProvider(logger.Named("identity"), identityDB, deserializerManager, binder, NewEIDRHDeserializer(), identityMetrics)
	identityProvider.SetSignerRouter(signerRouter)
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
	roleFactory.SetSignerRouter(signerRouter)

	kmps := make([]membership.KeyManagerProvider, 0, len(ppp.IdemixIssuerPublicKeys)+1)
	for _, key := range ppp.IdemixIssuerPublicKeys {
		idemixKeyStore, err := msp2.NewKeyStore(key.Curve, baseKeyStore)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to instantiate idemix key store")
		}
		kmp := idemixnym.NewKeyManagerProvider(
			key.PublicKey,
			key.Curve,
			idemixKeyStore,
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
	newRole, err = roleFactory.NewRole(identity.IssuerRole, false, ppp.Issuers(), x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create issuer role")
	}
	roles.Register(identity.IssuerRole, newRole)
	newRole, err = roleFactory.NewRole(identity.AuditorRole, false, ppp.Auditors(), x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
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
	signerRouter.SetConfIDResolver(walletDB)
	deser, err := NewDeserializer(ppp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to instantiate the deserializer")
	}

	return wallet.NewService(
		logger,
		identityProvider,
		deser,
		wallet.Convert(roles.Registries(logger, walletDB, role.NewDefaultFactory(logger, identityProvider, qe, identityConfig, deser, metricsProvider))),
	), nil
}

// WalletServiceFactory is a factory for creating zkatsnark wallet services.
type WalletServiceFactory struct {
	*BaseWalletServiceFactory

	storageProvider identity.StorageProvider
}

// NewWalletServiceFactory returns a new factory for the zkatsnark wallet service.
func NewWalletServiceFactory(storageProvider identity.StorageProvider) core.NamedFactory[driver.WalletServiceFactory] {
	return core.NamedFactory[driver.WalletServiceFactory]{
		Name: core.DriverIdentifier(driver.TokenDriverName("zkatsnark"), driver.TokenDriverVersion(1)),
		Driver: &WalletServiceFactory{
			BaseWalletServiceFactory: &BaseWalletServiceFactory{},
			storageProvider:          storageProvider},
	}
}

// NewWalletService returns a new zkatsnark wallet service for the passed configuration and public parameters.
func (d *WalletServiceFactory) NewWalletService(tmsConfig driver.Configuration, params driver.PublicParameters) (driver.WalletService, error) {
	tmsID := tmsConfig.ID()
	logger := logging.DriverLogger("panurus.driver.zkatsnark", tmsID.Network, tmsID.Channel, tmsID.Namespace)

	ppp, ok := params.(*pp.PublicParams)
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
		ppp,
		true,
		&disabled.Provider{},
	)
}

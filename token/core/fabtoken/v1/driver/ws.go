/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	v2 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/config"
	"github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	idriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/membership"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	"github.com/LFDT-Panurus/panurus/token/services/identity/wallet"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/observability"
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
	metricsProvider metrics.Provider,
) (*wallet.Service, error) {
	tmsID := tmsConfig.ID()
	fabPP, ok := pp.(*v2.PublicParams)
	if !ok {
		return nil, errors.Errorf("invalid public parameters type [%T]", pp)
	}
	if len(fabPP.X509OwnerRoots) == 0 {
		logger.Warnf("public parameters carry no x509 owner roots: x509 owner identities are not checked against a CA, any node can claim any enrollment ID")
	}
	eidrh, err := NewEIDRHDeserializerWithPublicParams(fabPP)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create enrollment ID deserializer")
	}

	deserializerManager := deserializer.NewTypedSignerDeserializerMultiplex()
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
	identityConfig, err := config.NewIdentityConfig(tmsConfig)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create identity config")
	}
	identityProvider := identity.NewProvider(
		logger.Named("identity"), identityDB, deserializerManager, binder, eidrh, identityMetrics,
	)
	identityProvider.SetSignerRouter(signerRouter)

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
	roleFactory.SetSignerRouter(signerRouter)
	newRole, err := roleFactory.NewRole(identity.OwnerRole, false, nil, x509.NewKeyManagerProvider(identityConfig, keyStore, ignoreRemote))
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create owner role")
	}
	if len(fabPP.X509OwnerRoots) != 0 {
		warnUntrustedOwners(context.Background(), logger, newRole, eidrh)
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
	signerRouter.SetConfIDResolver(walletDB)
	deserializer, err := NewDeserializerWithPublicParams(fabPP)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create deserializer")
	}
	ws := wallet.NewService(
		logger,
		identityProvider,
		deserializer,
		wallet.Convert(roles.Registries(logger, walletDB, role.NewDefaultFactory(logger, identityProvider, qe, identityConfig, deserializer, metricsProvider))),
	)

	return ws, nil
}

// warnUntrustedOwners logs a warning for each local owner identity that fails the x509 owner checks of eidrh.
func warnUntrustedOwners(ctx context.Context, logger logging.Logger, owners idriver.Role, eidrh *EIDRHDeserializer) {
	ids, err := owners.IdentityIDs()
	if err != nil {
		logger.Warnf("failed to list local owner identities: %v", err)

		return
	}
	for _, id := range ids {
		info, err := owners.GetIdentityInfo(ctx, id)
		if err != nil {
			logger.Warnf("failed to get local owner identity [%s]: %v", id, err)

			continue
		}
		ownerID, auditInfo, err := info.Get(ctx)
		if err != nil {
			logger.Warnf("failed to get local owner identity [%s]: %v", id, err)

			continue
		}
		if _, err := eidrh.GetEnrollmentID(ctx, ownerID, auditInfo); err != nil {
			logger.Warnf("local owner identity [%s] fails the x509 owner checks of the public parameters: %v", id, err)
		}
	}
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

	ws, err := d.newWalletService(
		tmsConfig,
		&membership.NoBinder{},
		d.storageProvider,
		nil,
		logger,
		nil,
		nil,
		params,
		true,
		&disabled.Provider{},
	)
	if err != nil {
		return nil, err
	}

	// Decorate with circuit breaker protection loaded from the TMS configuration.
	// A disabled.Provider is passed because the standalone WalletServiceFactory does
	// not have access to TMS-scoped metrics from the container.
	cbConfig := observability.LoadCircuitBreakerConfig(tmsConfig)

	return observability.NewWalletServiceDecoratorWithConfig(ws, &disabled.Provider{}, cbConfig), nil
}

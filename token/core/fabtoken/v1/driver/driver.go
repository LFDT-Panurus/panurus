/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	cdriver "github.com/LFDT-Panurus/panurus/token/core/common/driver"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	v1setup "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Driver contains the non-static logic of the fabtoken driver (including services).
type Driver struct {
	BaseWalletServiceFactory
	metricsProvider  cdriver.MetricsProvider
	tracerProvider   cdriver.TracerProvider
	configService    cdriver.ConfigService
	storageProvider  cdriver.StorageProvider
	identityProvider cdriver.IdentityProvider
	endpointService  cdriver.NetworkBinderService
	networkProvider  cdriver.NetworkProvider
	vaultProvider    cdriver.VaultProvider
}

// NewTokenDriver returns a new factory for the fabtoken driver.
func NewTokenDriver(
	metricsProvider cdriver.MetricsProvider,
	tracerProvider cdriver.TracerProvider,
	configService cdriver.ConfigService,
	storageProvider cdriver.StorageProvider,
	identityProvider cdriver.IdentityProvider,
	endpointService cdriver.NetworkBinderService,
	networkProvider cdriver.NetworkProvider,
	vaultProvider cdriver.VaultProvider,
) core.NamedFactory[driver.Driver] {
	return core.NamedFactory[driver.Driver]{
		Name: core.DriverIdentifier(v1setup.FabTokenDriverName, 1),
		Driver: newTokenDriver(
			metricsProvider,
			tracerProvider,
			configService,
			storageProvider,
			identityProvider,
			endpointService,
			networkProvider,
			vaultProvider,
		),
	}
}

func newTokenDriver(
	metricsProvider cdriver.MetricsProvider,
	tracerProvider cdriver.TracerProvider,
	configService cdriver.ConfigService,
	storageProvider cdriver.StorageProvider,
	identityProvider cdriver.IdentityProvider,
	endpointService cdriver.NetworkBinderService,
	networkProvider cdriver.NetworkProvider,
	vaultProvider cdriver.VaultProvider,
) *Driver {
	return &Driver{
		metricsProvider:  metricsProvider,
		tracerProvider:   tracerProvider,
		configService:    configService,
		storageProvider:  storageProvider,
		identityProvider: identityProvider,
		endpointService:  endpointService,
		networkProvider:  networkProvider,
		vaultProvider:    vaultProvider,
	}
}

// NewTokenService returns a new fabtoken token manager service for the passed TMS ID and public parameters.
func (d *Driver) NewTokenService(tmsID driver.TMSID, publicParams []byte) (driver.TokenManagerService, error) {
	logger := logging.DriverLogger("panurus.driver.fabtoken", tmsID.Network, tmsID.Channel, tmsID.Namespace)

	logger.Debugf("creating new token service with public parameters [%s]", utils.Hashable(publicParams))

	if len(publicParams) == 0 {
		return nil, errors.Errorf("empty public parameters")
	}

	// get network
	n, err := d.networkProvider.GetNetwork(tmsID.Network, tmsID.Channel)
	if err != nil {
		return nil, errors.Errorf("failed getting network [%s]", err)
	}

	// get vault
	vault, err := d.vaultProvider.Vault(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, errors.Errorf("failed getting vault [%s]", err)
	}

	tmsConfig, err := d.configService.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get config for token service for [%s:%s:%s]", tmsID.Network, tmsID.Channel, tmsID.Namespace)
	}

	publicParamsManager, err := common.NewPublicParamsManager[*v1setup.PublicParams](
		&PublicParamsDeserializer{},
		v1setup.FabTokenDriverName,
		v1setup.ProtocolV1,
		publicParams,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiliaze public params manager")
	}

	pp := publicParamsManager.PublicParams()
	logger.Infof("new token driver for tms id [%s] with label and version [%s:%s]: [%s]", tmsID, pp.TokenDriverName(), pp.TokenDriverVersion(), pp)

	networkLocalMembership := n.LocalMembership()
	qe := vault.QueryEngine()
	ws, err := d.newWalletService(
		tmsConfig,
		d.endpointService,
		d.storageProvider,
		qe,
		logger,
		d.identityProvider.DefaultIdentity(),
		networkLocalMembership.DefaultIdentity(),
		publicParamsManager.PublicParams(),
		false,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiliaze wallet service for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}
	deserializer := ws.Deserializer
	ip := ws.IdentityProvider

	authorization := common.NewAuthorizationMultiplexer(
		common.NewTMSAuthorization(logger, publicParamsManager.PublicParams(), ws),
		htlc.NewScriptAuth(ws),
		multisig.NewEscrowAuth(ws),
		boolpolicy.NewEscrowAuth(ws),
	)
	tokensService, err := v1.NewTokensService(publicParamsManager.PublicParams(), deserializer)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initialize token service for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}
	validator := validator.NewValidator(
		logger,
		publicParamsManager.PublicParams(),
		deserializer,
		nil,
		nil,
		nil,
	)
	metricsProvider := metrics.NewTMSProvider(tmsConfig.ID(), d.metricsProvider)
	service, err := v1.NewService(
		logger,
		ws,
		publicParamsManager,
		ip,
		deserializer,
		tmsConfig,
		metrics.NewIssueService(v1.NewIssueService(publicParamsManager, ws, deserializer), metricsProvider),
		metrics.NewTransferService(v1.NewTransferService(logger, publicParamsManager, ws, common.NewVaultTokenLoader(qe), deserializer), metricsProvider),
		metrics.NewAuditorService(v1.NewAuditorService(logger, publicParamsManager, deserializer, qe, d.tracerProvider), metricsProvider),
		metrics.NewTokensService(tokensService, metricsProvider),
		metrics.NewTokensUpgradeService(&v1.TokensUpgradeService{}, metricsProvider),
		authorization,
		validator,
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create token service")
	}

	return service, nil
}

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
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	v1setup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	v1token "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Driver contains the non-static logic of the zkatdlog driver (including services).
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

// NewTokenDriver returns a new factory for the zkatdlog driver.
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
		Name: core.DriverIdentifier(v1setup.DLogNoGHDriverName, v1setup.ProtocolV1),
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

// NewTokenService returns a new zkatdlog token manager service for the passed TMS ID and public parameters.
func (d *Driver) NewTokenService(tmsID driver.TMSID, publicParams []byte) (driver.TokenManagerService, error) {
	logger := logging.DriverLogger("panurus.driver.zkatdlog", tmsID.Network, tmsID.Channel, tmsID.Namespace)

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

	networkLocalMembership := n.LocalMembership()

	tmsConfig, err := d.configService.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get config for token service for [%s:%s:%s]", tmsID.Network, tmsID.Channel, tmsID.Namespace)
	}

	ppm, err := common.NewPublicParamsManager[*v1setup.PublicParams](
		&PublicParamsDeserializer{},
		v1setup.DLogNoGHDriverName,
		v1setup.ProtocolV1,
		publicParams,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiliaze public params manager")
	}

	pp := ppm.PublicParams()
	logger.Infof("new token driver for tms id [%s] with label and version [%s:%s]: [%s]", tmsID, pp.TokenDriverName(), pp.TokenDriverVersion(), pp)

	metricsProvider := metrics.NewTMSProvider(tmsConfig.ID(), d.metricsProvider)
	qe := vault.QueryEngine()
	ws, err := d.NewWalletService(
		tmsConfig,
		d.endpointService,
		d.storageProvider,
		qe,
		logger,
		d.identityProvider.DefaultIdentity(),
		networkLocalMembership.DefaultIdentity(),
		ppm.PublicParams(),
		false,
		metricsProvider,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiliaze wallet service for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}
	deserializer := ws.Deserializer
	ip := ws.IdentityProvider

	authorization := common.NewAuthorizationMultiplexer(
		common.NewTMSAuthorization(logger, ppm.PublicParams(), ws),
		htlc.NewScriptAuth(ws),
		multisig.NewEscrowAuth(ws),
		boolpolicy.NewEscrowAuth(ws),
	)

	tokensService, err := v1token.NewTokensService(logger, ppm, deserializer)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiliaze token service for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}
	tokensUpgradeService, err := upgrade.NewService(logger, ppm.PublicParams().QuantityPrecision, deserializer, ip)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiliaze token upgrade service for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}
	validator, err := validator.New(
		logger,
		ppm.PublicParams(),
		deserializer,
		nil,
		nil,
		nil,
	), nil
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}
	service, err := v1.NewTokenService(
		logger,
		ws,
		ppm,
		ip,
		deserializer,
		tmsConfig,
		metrics.NewIssueService(v1.NewIssueService(logger, ppm, ws, deserializer, tokensService, tokensUpgradeService), metricsProvider),
		metrics.NewTransferService(v1.NewTransferService(
			logger,
			ppm,
			ws,
			common.NewVaultLedgerTokenAndMetadataLoader[[]byte, []byte](qe, &common.IdentityTokenAndMetadataDeserializer{}),
			deserializer,
			d.tracerProvider,
			tokensService,
		), metricsProvider),
		metrics.NewAuditorService(v1.NewAuditorService(
			logger,
			ppm,
			deserializer,
			qe,
			d.tracerProvider,
		), metricsProvider),
		metrics.NewTokensService(tokensService, metricsProvider),
		metrics.NewTokensUpgradeService(tokensUpgradeService, metricsProvider),
		authorization,
		validator,
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create token service")
	}

	return service, err
}

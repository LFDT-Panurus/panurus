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
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/pp"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Driver contains the non-static logic of the zkatsnark driver (including services).
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

// NewTokenDriver returns a new factory for the zkatsnark driver.
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
		Name: core.DriverIdentifier(driver.TokenDriverName("zkatsnark"), driver.TokenDriverVersion(1)),
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

// NewTokenService returns a new zkatsnark token manager service for the passed TMS ID and public parameters.
func (d *Driver) NewTokenService(tmsID driver.TMSID, publicParams []byte) (driver.TokenManagerService, error) {
	logger := logging.DriverLogger("panurus.driver.zkatsnark", tmsID.Network, tmsID.Channel, tmsID.Namespace)

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

	ppm, err := common.NewPublicParamsManager[*pp.PublicParams](
		&PublicParamsDeserializer{},
		"zkatsnark",
		driver.TokenDriverVersion(1),
		publicParams,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initialize public params manager")
	}

	ppp := ppm.PublicParams()
	logger.Infof("new token driver for tms id [%s] with label and version [%s:%s]: [%s]", tmsID, ppp.TokenDriverName(), ppp.TokenDriverVersion(), ppp)

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
		return nil, errors.Wrapf(err, "failed to initialize wallet service for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}
	deserializer := ws.Deserializer
	ip := ws.IdentityProvider

	authorization := common.NewAuthorizationMultiplexer(
		common.NewTMSAuthorization(logger, ppm.PublicParams(), ws),
		htlc.NewScriptAuth(ws),
		multisig.NewEscrowAuth(ws),
		boolpolicy.NewEscrowAuth(ws),
	)

	valDriver := NewValidatorDriver().Driver
	validator, err := valDriver.NewValidator(ppp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}

	issueService := zkatsnark.NewIssueService(logger, ppm, ws, deserializer, nil, nil)
	transferService := zkatsnark.NewTransferService(logger, ppm, ws, nil, deserializer, d.tracerProvider, nil)
	auditorService := zkatsnark.NewAuditorService(logger, ppm, deserializer, qe, d.tracerProvider)

	service, err := zkatsnark.NewTokenService(
		logger,
		ws,
		ppm,
		ip,
		deserializer,
		tmsConfig,
		metrics.NewIssueService(issueService, metricsProvider),
		metrics.NewTransferService(transferService, metricsProvider),
		metrics.NewAuditorService(auditorService, metricsProvider),
		nil, // metrics.NewTokensService(tokensService, metricsProvider),
		nil, // metrics.NewTokensUpgradeService(tokensUpgradeService, metricsProvider),
		authorization,
		validator,
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create token service")
	}

	return service, err
}

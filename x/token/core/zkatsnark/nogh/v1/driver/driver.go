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
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/upgrade"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	zkatsnark "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/pp"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
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

	authorization := common.NewStandardAuthorization(logger, ppm.PublicParams(), ws)

	baseParams, err := prepared.NewParams(ppp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to prepare public parameters")
	}

	proverParams, err := prepared.NewProverFrom(baseParams)
	if err != nil {
		return nil, errors.Wrap(err, "failed to prepare public parameters for proving")
	}

	verifierParams, err := prepared.NewVerifierFrom(baseParams)
	if err != nil {
		return nil, errors.Wrap(err, "failed to prepare public parameters for verification")
	}

	val, err := validator.NewValidator(verifierParams, deserializer, driver.ResourceLimits{}.WithDefaults())
	if err != nil {
		return nil, errors.Wrap(err, "failed to instantiate validator")
	}

	spendProver := prover.NewSpendProver(proverParams)
	outputProver := prover.NewOutputProver(proverParams)
	orchestrator := prover.NewOrchestrator(spendProver, outputProver)

	tokensService, tokensUpgradeService, err := NewTokensServices(logger, ppm, deserializer, ip)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to initialize token services for [%s:%s]", tmsID.Network, tmsID.Namespace)
	}

	issueService := zkatsnark.NewIssueService(logger, ppm, ws, deserializer, tokensService, tokensUpgradeService, orchestrator, val)
	transferService := zkatsnark.NewTransferService(
		logger,
		ppm,
		ws,
		common.NewVaultLedgerTokenAndMetadataLoader[[]byte, []byte](qe, &common.IdentityTokenAndMetadataDeserializer{}),
		deserializer,
		d.tracerProvider,
		tokensService,
		tokensService,
		orchestrator,
		val,
	)
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
		metrics.NewTokensService(tokensService, metricsProvider),
		metrics.NewTokensUpgradeService(tokensUpgradeService, metricsProvider),
		authorization,
		val,
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to create token service")
	}

	return service, err
}

// NewTokensServices builds the TokensService and the TokensUpgradeService of a
// zkatsnark TMS, exactly as NewTokenService wires them: the upgrade service is
// eligible for exactly the fabtoken formats the tokens service does not spend
// in place, so each fabtoken format takes exactly one upgrade path.
func NewTokensServices(
	logger logging.Logger,
	ppm common.PublicParametersManager[*pp.PublicParams],
	deserializer driver.Deserializer,
	identityProvider upgrade.IdentityProvider,
) (*token.TokensService, *token.TokensUpgradeService, error) {
	tokensService, err := token.NewTokensService(logger, ppm, deserializer)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to initialize token service")
	}
	tokensUpgradeService, err := token.NewTokensUpgradeService(logger, ppm.PublicParams().PrecisionValue, deserializer, identityProvider, tokensService.SupportedTokenFormats())
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to initialize token upgrade service")
	}

	return tokensService, tokensUpgradeService, nil
}

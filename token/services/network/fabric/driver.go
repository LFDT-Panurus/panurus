/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"slices"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/common"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	config2 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/config"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"go.opentelemetry.io/otel/trace"
)

// NetworkPublicParamsFetcher models a public parameters fetcher per network.
type NetworkPublicParamsFetcher interface {
	// Fetch fetches the public parameters for the given network, channel, and namespace
	Fetch(network cdriver.Network, channel cdriver.Channel, namespace cdriver.Namespace) ([]byte, error)
}

type Driver struct {
	fnsProvider                     *fabric.NetworkServiceProvider
	tokensManager                   *tokens.ServiceManager
	configService                   *config.Service
	viewManager                     *view.Manager
	filterProvider                  *common.AcceptTxInDBFilterProvider
	tmsProvider                     *token.ManagementServiceProvider
	identityProvider                view.IdentityProvider
	tracerProvider                  trace.TracerProvider
	defaultPublicParamsFetcher      NetworkPublicParamsFetcher
	tokenQueryExecutorProvider      driver.TokenQueryExecutorProvider
	spentTokenQueryExecutorProvider driver.SpentTokenQueryExecutorProvider
	supportedDrivers                []string
	keyTranslator                   translator.KeyTranslator
	flmProvider                     finality.ListenerManagerProvider
	llmProvider                     lookup.ListenerManagerProvider
	EndorsementServiceProvider      EndorsementServiceProvider
	setupListenerProvider           SetupListenerProvider
	ttxStoreServiceManager          ttxdb.StoreServiceManager
	auditStoreServiceManager        auditdb.StoreServiceManager
	cleanupServiceManager           cleanup.ServiceManager
	metricsProvider                 metrics.Provider
}

func NewGenericDriver(
	fnsProvider *fabric.NetworkServiceProvider,
	tokensManager *tokens.ServiceManager,
	configProvider *config.Service,
	viewManager *view.Manager,
	viewRegistry *view.Registry,
	filterProvider *common.AcceptTxInDBFilterProvider,
	tmsProvider *token.ManagementServiceProvider,
	tracerProvider trace.TracerProvider,
	identityProvider view.IdentityProvider,
	configService cdriver.ConfigService,
	ttxStoreServiceManager ttxdb.StoreServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
	cleanupServiceManager cleanup.ServiceManager,
	endorserStoreServiceManager endorserdb.StoreServiceManager,
	metricsProvider metrics.Provider,
) driver.Driver {
	keyTranslator := &keys.Translator{}

	return NewDriver(
		fnsProvider,
		tokensManager,
		configProvider,
		viewManager,
		filterProvider,
		tmsProvider,
		tracerProvider,
		identityProvider,
		NewChaincodePublicParamsFetcher(viewManager),
		NewTokenExecutorProvider(fnsProvider),
		NewSpentTokenExecutorProvider(fnsProvider, keyTranslator),
		keyTranslator,
		finality.NewListenerManagerProvider(fnsProvider, tracerProvider, keyTranslator, config3.NewListenerManagerConfig(configService)),
		lookup.NewListenerManagerProvider(fnsProvider, tracerProvider, keyTranslator, config3.NewListenerManagerConfig(configService)),
		endorsement.NewServiceProvider(
			fnsProvider,
			tmsProvider,
			configProvider,
			viewManager,
			viewRegistry,
			identityProvider,
			keyTranslator,
			endorserStoreServiceManager,
		),
		NewSetupListenerProvider(tmsProvider, tokensManager),
		ttxStoreServiceManager,
		auditStoreServiceManager,
		cleanupServiceManager,
		metricsProvider,
		config2.GenericDriver,
	)
}

func NewDriver(
	fnsProvider *fabric.NetworkServiceProvider,
	tokensManager *tokens.ServiceManager,
	configService *config.Service,
	viewManager *view.Manager,
	filterProvider *common.AcceptTxInDBFilterProvider,
	tmsProvider *token.ManagementServiceProvider,
	tracerProvider trace.TracerProvider,
	identityProvider view.IdentityProvider,
	defaultPublicParamsFetcher NetworkPublicParamsFetcher,
	tokenQueryExecutorProvider driver.TokenQueryExecutorProvider,
	spentTokenQueryExecutorProvider driver.SpentTokenQueryExecutorProvider,
	keyTranslator translator.KeyTranslator,
	flmProvider finality.ListenerManagerProvider,
	llmProvider lookup.ListenerManagerProvider,
	endorsementServiceProvider EndorsementServiceProvider,
	setupListenerProvider SetupListenerProvider,
	ttxStoreServiceManager ttxdb.StoreServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
	cleanupServiceManager cleanup.ServiceManager,
	metricsProvider metrics.Provider,
	supportedDrivers ...string,
) *Driver {
	return &Driver{
		fnsProvider:                     fnsProvider,
		tokensManager:                   tokensManager,
		configService:                   configService,
		viewManager:                     viewManager,
		filterProvider:                  filterProvider,
		tmsProvider:                     tmsProvider,
		identityProvider:                identityProvider,
		tracerProvider:                  tracerProvider,
		defaultPublicParamsFetcher:      defaultPublicParamsFetcher,
		tokenQueryExecutorProvider:      tokenQueryExecutorProvider,
		spentTokenQueryExecutorProvider: spentTokenQueryExecutorProvider,
		supportedDrivers:                supportedDrivers,
		keyTranslator:                   keyTranslator,
		flmProvider:                     flmProvider,
		llmProvider:                     llmProvider,
		EndorsementServiceProvider:      endorsementServiceProvider,
		setupListenerProvider:           setupListenerProvider,
		ttxStoreServiceManager:          ttxStoreServiceManager,
		auditStoreServiceManager:        auditStoreServiceManager,
		cleanupServiceManager:           cleanupServiceManager,
		metricsProvider:                 metricsProvider,
	}
}

func (d *Driver) New(network, channel string) (driver.Network, error) {
	fns, err := d.fnsProvider.FabricNetworkService(network)
	if err != nil {
		return nil, errors.WithMessagef(err, "fabric network [%s] not found", network)
	}
	if !slices.Contains(d.supportedDrivers, fns.ConfigService().DriverName()) {
		return nil, errors.Errorf("only drivers [%s] supported. [%s] provided", d.supportedDrivers, fns.ConfigService().DriverName())
	}
	ch, err := fns.Channel(channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "fabric channel [%s:%s] not found", network, channel)
	}

	tokenQueryExecutor, err := d.tokenQueryExecutorProvider.GetExecutor(fns.Name(), ch.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get token query executor for [%s:%s]", fns.Name(), ch.Name())
	}
	spentTokenQueryExecutor, err := d.spentTokenQueryExecutorProvider.GetSpentExecutor(fns.Name(), ch.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get spent token query executor")
	}
	flm, err := d.flmProvider.NewManager(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create a new flm")
	}
	llm, err := d.llmProvider.NewManager(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create a new llm")
	}

	return NewNetwork(
		fns,
		ch,
		d.configService,
		d.filterProvider,
		d.tokensManager,
		d.viewManager,
		d.tmsProvider,
		d.EndorsementServiceProvider,
		tokenQueryExecutor,
		d.tracerProvider,
		d.defaultPublicParamsFetcher,
		spentTokenQueryExecutor,
		d.keyTranslator,
		flm,
		llm,
		d.setupListenerProvider,
		d.ttxStoreServiceManager,
		d.auditStoreServiceManager,
		d.cleanupServiceManager,
		d.metricsProvider,
		NewLedger(ch, fns.Name(), d.keyTranslator),
	), nil
}

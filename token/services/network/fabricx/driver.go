/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"slices"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/endorsement"
	finality2 "github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/queue"
	lookup2 "github.com/LFDT-Panurus/panurus/token/services/network/fabricx/lookup"
	pp2 "github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/qe"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	fabric2 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/committer/queryservice"
	finalityx "github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/finality"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"go.opentelemetry.io/otel/trace"
)

// NewDriver returns a new Driver instance for the FabricX network.
// It initializes core services including the query executor provider, listener managers,
// and endorsement service provider. It also validates that the finality type
// is set to "notification" and initializes the event queue for finality notifications.
func NewDriver(
	fnsProvider *fabric2.NetworkServiceProvider,
	tokensManager *tokens.ServiceManager,
	configs *config.Service,
	viewManager *view.Manager,
	tmsProvider *token.ManagementServiceProvider,
	tracerProvider trace.TracerProvider,
	identityProvider view.IdentityProvider,
	ppFetcher *pp2.PublicParametersService,
	configService cdriver.ConfigService,
	qsProvider queryservice.Provider,
	ttxStoreServiceManager ttxdb.StoreServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
	cleanupServiceManager cleanup.ServiceManager,
	endorserStoreServiceManager endorserdb.StoreServiceManager,
	queryServiceProvider queryservice.Provider,
	finalityProvider *finalityx.Provider,
	metricsProvider metrics.Provider,
	grpcClientProvider queryservice.GRPCClientProvider,
	ppValidator fsc.PublicParamsValidator,
) (driver.Driver, error) {
	vkp := pp2.NewVersionKeeperProvider()
	kt := &keys.Translator{}

	queryExecutorProvider := qe.NewExecutorProvider(qsProvider)

	// Load event queue configuration from token.finality.notification
	qCfg := queue.NewConfig(configService)
	q, err := queue.NewEventQueue(queue.Config{
		Workers:         qCfg.Workers(),
		QueueSize:       qCfg.QueueSize(),
		MetricsProvider: metricsProvider,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed creating event queue")
	}

	d := &Driver{
		ttxStoreServiceManager:     ttxStoreServiceManager,
		auditStoreServiceManager:   auditStoreServiceManager,
		cleanupServiceManager:      cleanupServiceManager,
		fnsProvider:                fnsProvider,
		tokensManager:              tokensManager,
		configService:              configs,
		viewManager:                viewManager,
		tmsProvider:                tmsProvider,
		tracerProvider:             tracerProvider,
		identityProvider:           identityProvider,
		defaultPublicParamsFetcher: ppFetcher,
		queryExecutorProvider:      queryExecutorProvider,
		keyTranslator:              kt,
		flmProvider: finality2.NewNotificationServiceBased(
			queryServiceProvider,
			finalityProvider,
			q,
			finality2.NewConfig(configService),
		),
		llmProvider: lookup2.NewCronNSListenerManagerProvider(
			queryServiceProvider,
			lookup2.NewConfig(configService),
		),
		EndorsementServiceProvider: endorsement.NewServiceProvider(
			configs,
			viewManager,
			viewManager,
			identityProvider,
			kt,
			vkp,
			tmsProvider,
			endorserStoreServiceManager,
			fnsProvider,
			grpcClientProvider,
			ppValidator,
		),
		setupListenerProvider: lookup2.NewSetupListenerProvider(
			tmsProvider,
			tokensManager,
			vkp,
		),
		supportedDrivers: []string{fabricx.DriverName},
		metricsProvider:  metricsProvider,
	}

	return d, nil
}

// Driver models the FabricX network driver.
type Driver struct {
	ttxStoreServiceManager     ttxdb.StoreServiceManager
	auditStoreServiceManager   auditdb.StoreServiceManager
	cleanupServiceManager      cleanup.ServiceManager
	fnsProvider                *fabric2.NetworkServiceProvider
	tokensManager              *tokens.ServiceManager
	configService              *config.Service
	viewManager                *view.Manager
	tmsProvider                *token.ManagementServiceProvider
	tracerProvider             trace.TracerProvider
	identityProvider           view.IdentityProvider
	defaultPublicParamsFetcher fabric.NetworkPublicParamsFetcher
	supportedDrivers           []string
	keyTranslator              translator.KeyTranslator
	flmProvider                finality.ListenerManagerProvider
	llmProvider                lookup.ListenerManagerProvider
	EndorsementServiceProvider fabric.EndorsementServiceProvider
	setupListenerProvider      fabric.SetupListenerProvider
	queryExecutorProvider      *qe.ExecutorProvider
	metricsProvider            metrics.Provider
}

// New returns a new Network instance for the specified network and channel.
// It retrieves the Fabric network service and channel, initializes the necessary
// query executors (token, spent token, and state) for that context,
// and sets up finality and lookup listener managers.
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

	tokenQueryExecutor, err := d.queryExecutorProvider.GetExecutor(fns.Name(), ch.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get token query executor for [%s:%s]", fns.Name(), ch.Name())
	}
	spentTokenQueryExecutor, err := d.queryExecutorProvider.GetSpentExecutor(fns.Name(), ch.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get spent token query executor")
	}
	queryStateExecutor, err := d.queryExecutorProvider.GetStateExecutor(fns.Name(), ch.Name())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get state query executor for [%s:%s]", fns.Name(), ch.Name())
	}
	flm, err := d.flmProvider.NewManager(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create a new flm")
	}
	llm, err := d.llmProvider.NewManager(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create a new llm")
	}

	logger.Debugf("fabricx network [%s:%s] with driver [%s] ready to be created...", network, channel, fns.ConfigService().DriverName())

	return NewNetwork(
		d.ttxStoreServiceManager,
		d.auditStoreServiceManager,
		d.cleanupServiceManager,
		fns,
		ch,
		d.configService,
		d.tokensManager,
		d.viewManager,
		d.tmsProvider,
		d.EndorsementServiceProvider,
		tokenQueryExecutor,
		d.tracerProvider,
		d.defaultPublicParamsFetcher,
		spentTokenQueryExecutor,
		queryStateExecutor,
		d.keyTranslator,
		flm,
		llm,
		d.setupListenerProvider,
		d.metricsProvider,
	), nil
}

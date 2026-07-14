/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabricx

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/network/common"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/qe"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	ffabric "github.com/hyperledger-labs/fabric-smart-client/platform/fabric"

	"go.opentelemetry.io/otel/trace"
)

// NewNetwork returns a new Network instance for the specified FabricX configuration.
// It initializes a base Fabric network and overrides its ledger with a FabricX-specific
// implementation that supports advanced state query executors.
func NewNetwork(
	storeServiceManager ttxdb.StoreServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
	cleanupServiceManager cleanup.ServiceManager,
	n *ffabric.NetworkService,
	ch *ffabric.Channel,
	configuration common.Configuration,
	filterProvider common.TransactionFilterProvider[*common.AcceptTxInDBsFilter],
	tokensProvider *tokens.ServiceManager,
	viewManager fabric.ViewManager,
	tmsProvider *token.ManagementServiceProvider,
	endorsementServiceProvider fabric.EndorsementServiceProvider,
	tokenQueryExecutor driver.TokenQueryExecutor,
	tracerProvider trace.TracerProvider,
	defaultPublicParamsFetcher fabric.NetworkPublicParamsFetcher,
	spentTokenQueryExecutor driver.SpentTokenQueryExecutor,
	queryStateExecutor qe.QueryStatesExecutor,
	keyTranslator translator.KeyTranslator,
	flm finality.ListenerManager,
	llm lookup.ListenerManager,
	setupListenerProvider fabric.SetupListenerProvider,
	metricsProvider metrics.Provider,
) *fabric.Network {
	// first create a fabric network
	tn := fabric.NewNetwork(
		n,
		ch,
		configuration,
		filterProvider,
		tokensProvider,
		viewManager,
		tmsProvider,
		endorsementServiceProvider,
		tokenQueryExecutor,
		tracerProvider,
		defaultPublicParamsFetcher,
		spentTokenQueryExecutor,
		keyTranslator,
		flm,
		llm,
		setupListenerProvider,
		storeServiceManager,
		auditStoreServiceManager,
		cleanupServiceManager,
		metricsProvider,
		NewLedger(ch, n.Name(), keyTranslator, queryStateExecutor),
	)

	return tn
}

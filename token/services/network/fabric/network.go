/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"
	"sync"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/encoding/json"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	ncommon "github.com/LFDT-Panurus/panurus/token/services/network/common"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	recovery2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/wrapper"
	ttxfinality "github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"go.opentelemetry.io/otel/trace"
)

const (
	// QueryPublicParamsFunction is the chaincode function name for querying public parameters.
	QueryPublicParamsFunction = "queryPublicParams"
	// QueryTokensFunctions is the chaincode function name for querying token data.
	QueryTokensFunctions = "queryTokens"
	// AreTokensSpent is the chaincode function name for checking if tokens are spent.
	AreTokensSpent = "areTokensSpent"
)

var logger = logging.MustGetLogger()

// GetTokensFunc is a function type that returns a token Service instance.
type GetTokensFunc = func() (*tokens.Service, error)

// GetTMSProviderFunc is a function type that returns a ManagementServiceProvider.
type GetTMSProviderFunc = func() *token2.ManagementServiceProvider

type lm struct {
	lm *fabric.LocalMembership
}

func (n *lm) DefaultIdentity() view.Identity {
	return n.lm.DefaultIdentity()
}

func (n *lm) AnonymousIdentity() (view.Identity, error) {
	return n.lm.AnonymousIdentity()
}

// ledger provides access to the Fabric ledger via the FSC fabric layer.
type ledger struct {
	l             *fabric.Ledger
	ch            *fabric.Channel
	network       string
	keyTranslator translator.KeyTranslator
}

func NewLedger(ch *fabric.Channel, network string, keyTranslator translator.KeyTranslator) *ledger {
	return &ledger{ch: ch, l: ch.Ledger(), network: network, keyTranslator: keyTranslator}
}

// Status retrieves the validation status of a transaction from the Fabric ledger.
func (l *ledger) Status(id string) (driver.ValidationCode, error) {
	tx, err := l.l.GetTransactionByID(id)
	if err != nil {
		return driver.Unknown, errors.Wrapf(err, "failed to get transaction [%s]", id)
	}
	logger.Debugf("ledger status of [%s] is [%d]", id, tx.ValidationCode())
	switch peer.TxValidationCode(tx.ValidationCode()) {
	case peer.TxValidationCode_VALID:
		return driver.Valid, nil
	default:
		return driver.Invalid, nil
	}
}

// GetTransactionStatus retrieves the current status and token request hash for a transaction.
func (l *ledger) GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error) {
	// Get the transaction from the ledger
	tx, err := l.l.GetTransactionByID(txID)
	if err != nil {
		return driver.Unknown, nil, "", errors.Wrapf(err, "failed to get transaction [%s]", txID)
	}

	// Map validation code to driver status
	validationCode := tx.ValidationCode()
	var txStatus driver.ValidationCode
	var statusMessage string
	switch peer.TxValidationCode(validationCode) {
	case peer.TxValidationCode_VALID:
		txStatus = driver.Valid
	default:
		txStatus = driver.Invalid
		statusMessage = peer.TxValidationCode(validationCode).String()
	}

	// If invalid, return early without token request hash
	if txStatus == driver.Invalid {
		return txStatus, nil, statusMessage, nil
	}

	// Extract token request hash from the transaction
	mapper := &finality.EndorserTxInfoMapper{
		Network:       l.network,
		KeyTranslator: l.keyTranslator,
	}
	txInfos, err := mapper.MapProcessedTx(tx)
	if err != nil {
		return driver.Unknown, nil, "", errors.Wrapf(err, "failed to map transaction [%s]", txID)
	}

	// Find the token request hash for the specified namespace
	for _, info := range txInfos {
		if info.Namespace == namespace {
			return info.Status, info.RequestHash, info.Message, nil
		}
	}

	// If no matching namespace found, return status without hash
	return txStatus, nil, statusMessage, nil
}

// GetStates performs a multi-key state query against the token chaincode.
func (l *ledger) GetStates(ctx context.Context, namespace string, keys ...string) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, errors.Errorf("keys cannot be empty")
	}
	arg, err := json.Marshal(keys)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling args for query by ids [%v]", keys)
	}
	logger.DebugfContext(ctx, "querying chaincode [%s] for the states of ids [%v]", namespace, keys)
	chaincode := l.ch.Chaincode(namespace)
	res, err := chaincode.Query(lookup.QueryStates, arg).Query()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to query for states of ids [%v]", keys)
	}
	var values [][]byte
	err = json.Unmarshal(res, &values)
	if err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling results for query by ids [%v", keys)
	}

	return values, nil
}

func (l *ledger) TransferMetadataKey(k string) (string, error) {
	return l.keyTranslator.CreateTransferActionMetadataKey(k)
}

// ViewManager models the interface for initiating FSC views.
type ViewManager interface {
	InitiateView(ctx context.Context, view view.View) (any, error)
}

// ViewRegistry models the interface for registering view responders.
type ViewRegistry interface {
	RegisterResponder(responder view.View, initiatedBy any) error
}

// EndorsementService models the interface for transaction endorsement.
type EndorsementService = endorsement.Service

// EndorsementServiceProvider provides endorsement services for different TMS IDs.
type EndorsementServiceProvider = lazy.Provider[token2.TMSID, EndorsementService]

// SetupListenerProvider defines the interface for obtaining lookup listeners for public parameters setup.
type SetupListenerProvider interface {
	GetListener(token2.TMSID) lookup.Listener
}

// Network implements the driver.Network interface for Hyperledger Fabric.
// It orchestrates finality listeners, state queries, and endorsement requests.
type Network struct {
	n                        *fabric.NetworkService
	ch                       *fabric.Channel
	tmsProvider              *token2.ManagementServiceProvider
	viewManager              ViewManager
	ledger                   driver.Ledger
	configuration            ncommon.Configuration
	filterProvider           ncommon.TransactionFilterProvider[*ncommon.AcceptTxInDBsFilter]
	tokensProvider           *tokens.ServiceManager
	finalityTracer           trace.Tracer
	localMembership          *lm
	auditStoreServiceManager auditdb.StoreServiceManager
	ttxStoreServiceManager   ttxdb.StoreServiceManager
	cleanupServiceManager    cleanup.ServiceManager
	metricsProvider          metrics.Provider

	setupListenerProvider      SetupListenerProvider
	flm                        finality.ListenerManager
	llm                        lookup.ListenerManager
	defaultPublicParamsFetcher NetworkPublicParamsFetcher
	tokenQueryExecutor         driver.TokenQueryExecutor
	spentTokenQueryExecutor    driver.SpentTokenQueryExecutor
	endorsementServiceProvider EndorsementServiceProvider
	keyTranslator              translator.KeyTranslator

	connectedNamespaces lazy.Provider[string, []token2.ServiceOption]
}

// NewNetwork creates a new Fabric Network instance.
func NewNetwork(
	n *fabric.NetworkService,
	ch *fabric.Channel,
	configuration ncommon.Configuration,
	filterProvider ncommon.TransactionFilterProvider[*ncommon.AcceptTxInDBsFilter],
	tokensProvider *tokens.ServiceManager,
	viewManager ViewManager,
	tmsProvider *token2.ManagementServiceProvider,
	endorsementServiceProvider EndorsementServiceProvider,
	tokenQueryExecutor driver.TokenQueryExecutor,
	tracerProvider trace.TracerProvider,
	defaultPublicParamsFetcher NetworkPublicParamsFetcher,
	spentTokenQueryExecutor driver.SpentTokenQueryExecutor,
	keyTranslator translator.KeyTranslator,
	flm finality.ListenerManager,
	llm lookup.ListenerManager,
	setupListenerProvider SetupListenerProvider,
	ttxStoreServiceManager ttxdb.StoreServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
	cleanupServiceManager cleanup.ServiceManager,
	metricsProvider metrics.Provider,
	ledger driver.Ledger,
) *Network {
	network := &Network{
		n:                          n,
		ch:                         ch,
		tmsProvider:                tmsProvider,
		viewManager:                viewManager,
		ledger:                     ledger,
		configuration:              configuration,
		filterProvider:             filterProvider,
		tokensProvider:             tokensProvider,
		flm:                        flm,
		llm:                        llm,
		defaultPublicParamsFetcher: defaultPublicParamsFetcher,
		endorsementServiceProvider: endorsementServiceProvider,
		tokenQueryExecutor:         tokenQueryExecutor,
		spentTokenQueryExecutor:    spentTokenQueryExecutor,
		finalityTracer: tracerProvider.Tracer("finality_listener", tracing.WithMetricsOpts(tracing.MetricsOpts{
			LabelNames: []tracing.LabelName{},
		})),
		keyTranslator:            keyTranslator,
		setupListenerProvider:    setupListenerProvider,
		localMembership:          &lm{lm: n.LocalMembership()},
		ttxStoreServiceManager:   ttxStoreServiceManager,
		auditStoreServiceManager: auditStoreServiceManager,
		cleanupServiceManager:    cleanupServiceManager,
		metricsProvider:          metricsProvider,
	}
	network.connectedNamespaces = lazy.NewProviderWithKeyMapper(func(s string) string {
		return s
	}, network.connect)

	return network
}

// Name returns the name of the Fabric network.
func (n *Network) Name() string {
	return n.n.Name()
}

// Channel returns the name of the Fabric channel.
func (n *Network) Channel() string {
	return n.ch.Name()
}

// Normalize ensures that network, channel, and namespace are correctly set in the options.
func (n *Network) Normalize(opt *token2.ServiceOptions) (*token2.ServiceOptions, error) {
	if len(opt.Network) == 0 {
		opt.Network = n.n.Name()
	}
	if opt.Network != n.n.Name() {
		return nil, errors.Errorf("invalid network [%s], expected [%s]", opt.Network, n.n.Name())
	}

	if len(opt.Channel) == 0 {
		opt.Channel = n.ch.Name()
	}
	if opt.Channel != n.ch.Name() {
		return nil, errors.Errorf("invalid channel [%s], expected [%s]", opt.Channel, n.ch.Name())
	}

	if len(opt.Namespace) == 0 {
		if ns, err := n.configuration.LookupNamespace(opt.Network, opt.Channel); err == nil {
			logger.Debugf("no namespace specified, found namespace [%s] for [%s:%s]", ns, opt.Network, opt.Channel)
			opt.Namespace = ns
		} else {
			logger.Errorf("no namespace specified, and no default namespace found [%s], use default [%s]", err, ttx.TokenNamespace)
			opt.Namespace = ttx.TokenNamespace
		}
	}
	if opt.PublicParamsFetcher == nil {
		opt.PublicParamsFetcher = ncommon.NewPublicParamsFetcher(n, opt.Namespace)
	}

	return opt, nil
}

// Connect initializes listeners for public parameters and initializes the endorsement service for the namespace.
func (n *Network) Connect(ns string) (opts []token2.ServiceOption, err error) {
	return n.connectedNamespaces.Get(ns)
}

// Broadcast sends a transaction envelope to the ordering service.
func (n *Network) Broadcast(ctx context.Context, blob any) error {
	return n.n.Ordering().Broadcast(ctx, blob)
}

// NewEnvelope returns a new transaction envelope for the Fabric network.
func (n *Network) NewEnvelope() driver.Envelope {
	return n.n.TransactionManager().NewEnvelope()
}

// RequestApproval requests an endorsement for a token request.
func (n *Network) RequestApproval(context view.Context, tms *token2.ManagementService, requestRaw []byte, signer view.Identity, txID driver.TxID, metadata driver.TransientMap) (driver.Envelope, error) {
	endorsement, err := n.endorsementServiceProvider.Get(tms.ID())
	if err != nil {
		return nil, errors.Wrapf(err, "network not connected [%s]", tms.ID())
	}

	return endorsement.Endorse(context, requestRaw, signer, txID, metadata)
}

// ComputeTxID calculates the Fabric transaction ID based on creator and nonce.
func (n *Network) ComputeTxID(id *driver.TxID) string {
	logger.Debugf("compute tx id for [%s]", id.String())
	temp := &fabric.TxID{
		Nonce:   id.Nonce,
		Creator: id.Creator,
	}
	res := n.n.TransactionManager().ComputeTxID(temp)
	id.Nonce = temp.Nonce
	id.Creator = temp.Creator

	return res
}

// FetchPublicParameters queries the chaincode for the current public parameters.
func (n *Network) FetchPublicParameters(namespace string) ([]byte, error) {
	return n.defaultPublicParamsFetcher.Fetch(n.Name(), n.Channel(), namespace)
}

// QueryTokens retrieves token data from the global state.
func (n *Network) QueryTokens(ctx context.Context, namespace string, IDs []*token.ID) ([][]byte, error) {
	return n.tokenQueryExecutor.QueryTokens(ctx, namespace, IDs)
}

// AreTokensSpent checks if tokens have been consumed by verifying their existence in the ledger.
func (n *Network) AreTokensSpent(ctx context.Context, namespace string, tokenIDs []*token.ID, meta []string) ([]bool, error) {
	return n.spentTokenQueryExecutor.QuerySpentTokens(ctx, namespace, tokenIDs, meta)
}

// LocalMembership returns the membership service for the local FSC node.
func (n *Network) LocalMembership() driver.LocalMembership {
	return n.localMembership
}

// AddFinalityListener registers a callback for transaction status updates.
func (n *Network) AddFinalityListener(namespace string, txID string, listener driver.FinalityListener) error {
	return n.flm.AddFinalityListener(namespace, txID, listener)
}

// GetTransactionStatus retrieves the current status and token request hash for a transaction.
// It delegates to the ledger implementation.
func (n *Network) GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error) {
	return n.ledger.GetTransactionStatus(ctx, namespace, txID)
}

// LookupTransferMetadataKey performs a scan to find transfer metadata matching a sub-key.
func (n *Network) LookupTransferMetadataKey(namespace string, key string, timeout time.Duration) ([]byte, error) {
	transferMetadataKey, err := n.keyTranslator.CreateTransferActionMetadataKey(key)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate transfer action metadata key from [%s]", key)
	}
	logger.Debugf("lookup transfer metadata key [%s] from [%s] in namespace [%s]", key, transferMetadataKey, namespace)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	l := &lookupListener{wg: wg, key: transferMetadataKey}
	if err := n.llm.AddLookupListener(namespace, transferMetadataKey, l); err != nil {
		return nil, errors.Wrapf(err, "failed to add lookup listener")
	}
	defer func() {
		if err := n.llm.RemoveLookupListener(transferMetadataKey, l); err != nil {
			logger.Debugf("failed to remove lookup listener [%s]: %v", transferMetadataKey, err)
		}
	}()
	if err := waitTimeout(wg, timeout); err != nil {
		return nil, err
	}
	logger.Debugf("lookup transfer metadata key [%s] from [%s] in namespace [%s], done, result [%s][%s]", key, transferMetadataKey, namespace, l.value, l.err)

	return l.value, l.err
}

// Ledger returns direct access to the ledger querying layer.
func (n *Network) Ledger() (driver.Ledger, error) {
	return n.ledger, nil
}

func (n *Network) connect(ns string) ([]token2.ServiceOption, error) {
	tmsID := token2.TMSID{
		Network:   n.n.Name(),
		Channel:   n.ch.Name(),
		Namespace: ns,
	}

	setUpKey, err := n.keyTranslator.CreateSetupKey()
	if err != nil {
		return nil, errors.Errorf("failed creating setup key")
	}
	if err := n.llm.AddPermanentLookupListener(ns, setUpKey, n.setupListenerProvider.GetListener(tmsID)); err != nil {
		return nil, errors.Errorf("failed adding setup key listener")
	}

	// Let the endorsement service initialize itself, if needed
	if _, err := n.endorsementServiceProvider.Get(tmsID); err != nil {
		return nil, errors.WithMessagef(err, "failed to get endorsement service at [%s]", tmsID)
	}

	// Initialize and start recovery managers
	tokensService, err := n.tokensProvider.ServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get tokens service for [%s]", tmsID)
	}

	{
		storage, err := n.ttxStoreServiceManager.StoreServiceByTMSId(tmsID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get ttx storage for [%s]", tmsID)
		}
		_, err = n.createRecoveryManager(tmsID, storage, tokensService)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create recovery manager for ttx storage for [%s]", tmsID)
		}
	}

	{
		storage, err := n.auditStoreServiceManager.StoreServiceByTMSId(tmsID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get audit storage for [%s]", tmsID)
		}
		_, err = n.createRecoveryManager(tmsID, storage, tokensService)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create recovery manager for audit storage for [%s]", tmsID)
		}
	}

	// Initialize cleanup manager for token storage
	_, err = n.createCleanupManager(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create cleanup manager for [%s]", tmsID)
	}

	return nil, nil
}

// createRecoveryManager initializes and starts the recovery manager for the given namespace
func (n *Network) createRecoveryManager(tmsID token2.TMSID, storage transactionDB, tokensService *tokens.Service) (*recovery2.Manager, error) {
	// Get TMS configuration
	cfg, err := n.configuration.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		logger.Warnf("failed to get configuration for [%s], using default recovery config: %v", tmsID, err)
		cfg = nil
	}

	// Load recovery configuration
	var recoveryConfig recovery2.Config
	if cfg != nil {
		recoveryConfig, err = recovery2.LoadConfig(cfg)
		if err != nil {
			logger.Warnf("failed to load recovery config for [%s], using defaults: %v", tmsID, err)
			recoveryConfig = recovery2.DefaultConfig()
		}
	} else {
		recoveryConfig = recovery2.DefaultConfig()
	}

	// Create recovery handler with all required dependencies
	recoveryHandler := ttxfinality.NewTTXRecoveryHandler(
		logger,
		n,
		tmsID.Namespace,
		ttxfinality.NewTokenRequestHasher(wrapper.NewTokenManagementServiceProvider(n.tmsProvider), tmsID),
		tmsID,
		storage,
		tokensService,
		n.finalityTracer,
		n.metricsProvider,
	)

	manager := recovery2.NewManager(
		logger,
		storage,
		recoveryHandler,
		recoveryConfig,
	)

	// Start the recovery manager
	if err := manager.Start(); err != nil {
		return nil, errors.Wrapf(err, "failed to start recovery manager for [%s]", tmsID)
	}

	logger.Debugf("recovery manager started for namespace [%s]", tmsID.Namespace)

	return manager, nil
}

// createCleanupManager initializes and starts the cleanup manager for the given namespace
func (n *Network) createCleanupManager(tmsID token2.TMSID) (*cleanup.Manager, error) {
	// Use the cleanup service manager to get the manager for this TMS ID
	manager, err := n.cleanupServiceManager.ServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get cleanup manager for [%s]", tmsID)
	}

	logger.Debugf("cleanup manager retrieved for namespace [%s]", tmsID.Namespace)

	return manager, nil
}

type lookupListener struct {
	key   string
	wg    *sync.WaitGroup
	value []byte
	err   error
}

func (l *lookupListener) OnStatus(ctx context.Context, key string, value []byte) {
	logger.DebugfContext(ctx, "lookup transfer metadata key [%s], got value [%s][%v]", l.key, key, value)
	if l.key == key {
		l.value = value
		l.wg.Done()

		return
	}
}

func (l *lookupListener) OnError(ctx context.Context, key string, err error) {
	logger.DebugfContext(ctx, "lookup transfer metadata key [%s], got error [%s][%s]", l.key, key, err)
	if l.key == key {
		l.err = err
		l.wg.Done()

		return
	}
}

func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) error {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	select {
	case <-c:
		return nil
	case <-time.After(timeout):
		return errors.Errorf("context done")
	}
}

// NewSetupListenerProvider returns a provider for listeners that monitor public parameter updates.
func NewSetupListenerProvider(tmsProvider *token2.ManagementServiceProvider, tokensProvider *tokens.ServiceManager) *setupListenerProvider {
	return &setupListenerProvider{
		tmsProvider:    tmsProvider,
		tokensProvider: tokensProvider,
	}
}

type setupListenerProvider struct {
	tmsProvider    *token2.ManagementServiceProvider
	tokensProvider *tokens.ServiceManager
}

// GetListener returns a setupListener configured for the specified TMS ID.
func (p *setupListenerProvider) GetListener(tmsID token2.TMSID) lookup.Listener {
	return &setupListener{
		GetTMSProvider: func() *token2.ManagementServiceProvider { return p.tmsProvider },
		GetTokens: lazy.NewGetter[*tokens.Service](func() (*tokens.Service, error) {
			return p.tokensProvider.ServiceByTMSId(tmsID)
		}).Get,
		TMSID: tmsID,
	}
}

type setupListener struct {
	GetTMSProvider GetTMSProviderFunc
	GetTokens      GetTokensFunc
	TMSID          token2.TMSID
}

// OnStatus is triggered when the setup key (public parameters) is updated on the ledger.
func (s *setupListener) OnStatus(ctx context.Context, key string, value []byte) {
	logger.Infof("update TMS [%s] with key-value [%s][%s]", s.TMSID, key, utils.Hashable(value))
	tsmProvider := s.GetTMSProvider()
	if err := tsmProvider.Update(s.TMSID, value); err != nil {
		logger.Warnf("failed to update TMS [%s]: [%v]", key, err)
	}
	tokens, err := s.GetTokens()
	if err != nil {
		logger.Warnf("failed to get tokens db [%v]", err)

		return
	}
	if err := tokens.StorePublicParams(ctx, value); err != nil {
		logger.Warnf("failed to store public parameter key [%s]: [%v]", key, err)
	}
}

func (s *setupListener) OnError(ctx context.Context, key string, err error) {
	logger.Warnf("setup listener error for TMS [%s] key [%s]: [%v]", s.TMSID, key, err)
}

type transactionDB interface {
	NewTransaction() (storage.TransactionStoreTransaction, error)
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error
	AcquireRecoveryLeadership(ctx context.Context, lockID int64) (recovery2.Leadership, bool, error)
	ClaimPendingTransactions(ctx context.Context, olderThan time.Duration, leaseDuration time.Duration, limit int, owner string) ([]*ttxdb.RecoveryClaim, error)
	ReleaseRecoveryClaim(ctx context.Context, txID string, owner string, message string) error
}

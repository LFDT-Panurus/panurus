/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"strings"
	"sync"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/pp"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"go.opentelemetry.io/otel/trace"
)

var logger = logging.MustGetLogger()

// EVMConfigKey is the TMS-scoped configuration key under which the EVM network service is declared,
// i.e. token.tms.<tms-id>.services.network.evm. Its presence marks a TMS as an EVM network.
const EVMConfigKey = "services.network.evm"

// networkResolver decides whether a (network, channel) pair is served by the EVM driver, and yields
// that network's configuration. It is a small seam over the configuration service so the driver's
// routing can be unit-tested without a full config service.
type networkResolver interface {
	// IsEVMNetwork reports whether the given network/channel has an EVM network configuration.
	IsEVMNetwork(network, channel string) bool
	// ConfigFor returns the EVM configuration of the first TMS declaring the given network/channel.
	// It is used only to bootstrap the one thing every TMS on a network must agree on - the endpoint
	// and chain id, checked for agreement across every TMS in Driver.New - never for anything per-TMS,
	// since two TMS sharing a network are not required to share their contracts, endorsement policy,
	// submitter or gas policy. ConfigForTMS is what resolves those.
	ConfigFor(network, channel string) (*Config, error)
	// ConfigForTMS returns one TMS's own EVM configuration, read from its own services.network.evm
	// block rather than whichever TMS happened to declare the network first.
	ConfigForTMS(tmsID token2.TMSID) (*Config, error)
	// TMSIDsFor returns every TMS configured on the given network/channel. Unlike ConfigFor, which
	// takes the first match because the endpoint and chain are network-wide, a public-parameters
	// update has to reach each TMS individually.
	TMSIDsFor(network, channel string) []token2.TMSID
	// ConfigurationFor returns the raw token-sdk configuration of one TMS, for the settings this
	// driver reads through the shared loaders rather than its own.
	ConfigurationFor(tmsID token2.TMSID) (*config.Configuration, error)
}

// Driver is the EVM network driver factory. It implements driver.Driver: the network provider calls
// New for every (network, channel) and uses the first driver that returns no error, so New must
// return an error for networks that are not configured for EVM.
type Driver struct {
	resolver networkResolver
	// membership owns identities the driver does not mint.
	membership driver.LocalMembership
	// identities resolves the node names the configuration carries into the identities sessions are
	// actually opened to and authenticated against.
	identities view.IdentityProvider
	// viewManager and viewRegistry drive the endorsement flow over FSC sessions.
	viewManager  endorsement.ViewManager
	viewRegistry endorsement.ViewRegistry
	// tmsProvider resolves a TMS when an endorsement request names one. It is used only at request
	// time: resolving during construction would close a cycle, since building a TMS goes through this
	// driver.
	tmsProvider *token2.ManagementServiceProvider
	// tokensManager gives the token store for a TMS, so a parameters update is persisted as well as
	// applied to the running service.
	tokensManager *tokens.ServiceManager
	// ttxStores and auditStores are the stores transaction recovery sweeps: a node that restarts
	// loses the in-memory finality listeners that would have completed its pending transactions.
	ttxStores       ttxdb.StoreServiceManager
	auditStores     auditdb.StoreServiceManager
	metricsProvider metrics.Provider
	recoveryTracer  trace.Tracer
	// registerMu guards registeredFor: this node can register only one FSC-level endorsement responder
	// for its whole lifetime (see registerEndorser), so a second, different network trying to claim it
	// needs to be detected rather than silently discarded.
	registerMu    sync.Mutex
	registeredFor string
	// watchers keeps one public-parameters watcher per distinct TokenState contract (not per network:
	// two TMS on one network can point at two different contracts and need two watchers), so building
	// a network twice, or two TMS sharing one contract, does not leave two pollers on one contract.
	watchersMu sync.Mutex
	watchers   map[string]*pp.Watcher
	// recoveries keeps the sweeps started per TMS, for the same reason.
	recoveryMu sync.Mutex
	recoveries map[string][]*recovery.Manager
}

// Compile-time assertion that Driver satisfies the factory contract.
var _ driver.Driver = (*Driver)(nil)

// NewDriver returns a new EVM network Driver. It is wired into the SDK dig container under the
// "network-drivers" group (see the evmdlog SDK module), which supplies both arguments.
//
// The identity provider is required rather than optional: the token drivers read
// LocalMembership().DefaultIdentity() while constructing a TMS, so without it a node fails at
// startup rather than at its first transaction.
func NewDriver(
	configService *config.Service,
	identityProvider view.IdentityProvider,
	viewManager *view.Manager,
	viewRegistry *view.Registry,
	tmsProvider *token2.ManagementServiceProvider,
	tokensManager *tokens.ServiceManager,
	ttxStores ttxdb.StoreServiceManager,
	auditStores auditdb.StoreServiceManager,
	tracerProvider trace.TracerProvider,
	metricsProvider metrics.Provider,
) driver.Driver {
	return &Driver{
		resolver:      &configNetworkResolver{cs: configService},
		membership:    newLocalMembership(identityProvider),
		identities:    identityProvider,
		viewManager:   viewManager,
		viewRegistry:  viewRegistry,
		tmsProvider:   tmsProvider,
		tokensManager: tokensManager,
		ttxStores:     ttxStores,
		auditStores:   auditStores,
		recoveryTracer: tracerProvider.Tracer("finality_listener", tracing.WithMetricsOpts(tracing.MetricsOpts{
			LabelNames: []tracing.LabelName{},
		})),
		metricsProvider: metricsProvider,
		watchers:        map[string]*pp.Watcher{},
		recoveries:      map[string][]*recovery.Manager{},
	}
}

// New returns an EVM Network for the given network/channel, or an error if that network is not
// configured for EVM (so the network provider falls through to the next registered driver).
//
// The chain client is genuinely network-scoped: one endpoint, shared by every TMS declaring this
// network/channel. Everything else this method builds - the submitter, the reader inside NewNetwork,
// the endorsement domain - is resolved per TMS instead, from that TMS's own configuration, because
// token/services/network.Provider memoizes one *Network per (network, channel) and reuses it for
// every TMS sharing that pair: building only one, network-wide instance of anything TMS-specific
// would make every TMS after the first one silently read, write and sign through whichever TMS's
// configuration happened to be resolved first. Local membership is supplied separately by the SDK
// wiring, whose identities the driver does not own.
func (d *Driver) New(network, channel string) (driver.Network, error) {
	if !d.resolver.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
	}
	logger.Debugf("creating evm network [%s:%s]", network, channel)

	tmsIDs := d.resolver.TMSIDsFor(network, channel)
	if len(tmsIDs) == 0 {
		return nil, errors.Errorf("evm: no tms declares network [%s:%s]", network, channel)
	}

	networkConfig, err := d.resolver.ConfigFor(network, channel)
	if err != nil {
		return nil, err
	}
	evmClient, err := client.NewJSONRPCClient(networkConfig.Endpoint, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "evm: failed to create a client for [%s:%s]", network, channel)
	}

	namespaces := make([]NamespaceConfig, 0, len(tmsIDs))
	for _, tmsID := range tmsIDs {
		tmsConfig, err := d.resolver.ConfigForTMS(tmsID)
		if err != nil {
			return nil, errors.Wrapf(err, "evm: failed to load the configuration for [%s]", tmsID)
		}
		// The chain client above is built once, for the whole network, so every TMS sharing it has to
		// actually agree on what it connects to. Catching a disagreement here, at startup, is the same
		// contract this driver already holds for a bad endpoint or chain id on a single TMS; without
		// it a second TMS configuring a different endpoint would silently keep talking to the first
		// TMS's chain instead.
		if tmsConfig.Endpoint != networkConfig.Endpoint || tmsConfig.ChainID != networkConfig.ChainID {
			return nil, errors.Errorf(
				"evm: tms [%s] configures endpoint [%s] and chain id %d, but [%s:%s] is already serving "+
					"endpoint [%s] and chain id %d; every TMS on one EVM network must agree on the chain "+
					"it connects to", tmsID, tmsConfig.Endpoint, tmsConfig.ChainID, network, channel,
				networkConfig.Endpoint, networkConfig.ChainID)
		}

		submitter, err := d.newSubmitter(tmsConfig, evmClient)
		if err != nil {
			return nil, err
		}
		namespaces = append(namespaces, NamespaceConfig{
			Namespace: tmsID.Namespace,
			Config:    tmsConfig,
			Submitter: submitter,
		})
	}

	n, err := NewNetwork(network, evmClient, namespaces, nil, d.membership)
	if err != nil {
		return nil, err
	}
	if err := d.installEndorsement(n, namespaces, evmClient, network, channel); err != nil {
		return nil, err
	}
	d.watchPublicParams(network, channel, namespaces, evmClient)

	// Recovery starts when a namespace binds to the network rather than here, because it is per TMS
	// and the namespace is only known then. Mirrors the Fabric driver, which starts it in connect.
	n.SetRecoveryStarter(func(ns string) error {
		return d.startRecovery(token2.TMSID{Network: network, Channel: channel, Namespace: ns}, n)
	})

	return n, nil
}

// watchPublicParams starts watching the chain for public-parameters updates, one watcher per distinct
// TokenState contract among namespaces, each fanning its updates out only to the TMS actually
// deployed against that contract.
//
// Parameters change through an endorsed setup delta that some other node submits, so there is nothing
// local to trigger off: without this a node keeps serving whatever it started with. Fabric gets the
// same signal from a listener on the setup key.
//
// Grouping by contract rather than watching once per network matters because two TMS sharing a
// network are not required to share a TokenState: fanning every update out to every TMS on the
// network (as if they all watched the same contract) would push one TMS's parameters into another
// TMS's store the moment their contracts diverge. Two TMS that do happen to point at the very same
// contract are grouped together and correctly share one watcher and one fan-out.
func (d *Driver) watchPublicParams(network, channel string, namespaces []NamespaceConfig, evmClient client.EVMClient) {
	if d.tmsProvider == nil {
		logger.Debugf("no token management service provider; this node cannot reload public parameters")

		return
	}

	byContract := map[client.Address][]token2.TMSID{}
	configByContract := map[client.Address]*Config{}
	for _, nc := range namespaces {
		tokenState, err := nc.Config.TokenStateAddress()
		if err != nil {
			logger.Errorf("cannot watch public parameters for [%s:%s:%s]: %v", network, channel, nc.Namespace, err)

			continue
		}
		tmsID := token2.TMSID{Network: network, Channel: channel, Namespace: nc.Namespace}
		byContract[tokenState] = append(byContract[tokenState], tmsID)
		configByContract[tokenState] = nc.Config
	}

	d.watchersMu.Lock()
	defer d.watchersMu.Unlock()
	for tokenState, tmsIDs := range byContract {
		key := network + ":" + channel + ":" + tokenState.Hex()
		if _, running := d.watchers[key]; running {
			continue
		}
		cfg := configByContract[tokenState]

		watcher, err := pp.NewWatcher(
			evmClient, tokenState, cfg.Finality.BlockTag, cfg.Finality.PollInterval,
			func(ctx context.Context, raw []byte, version uint64) error {
				return d.applyPublicParams(ctx, tmsIDs, raw, version)
			},
		)
		if err != nil {
			logger.Errorf("cannot watch public parameters for [%s]: %v", key, err)

			continue
		}
		watcher.Start(context.Background())
		d.watchers[key] = watcher
	}
}

// applyPublicParams reloads every TMS on the network with the new parameters and persists them. A
// failure for one TMS does not stop the others: they are independent, and a node serving stale
// parameters for one is better than for all of them.
//
// It returns the combined error of every TMS that failed, if any, so the watcher knows this version
// was not fully applied and retries it rather than treating it as handled. Retrying is safe: Update is
// a no-op when the parameters it is given already match the TMS's current ones, so a TMS that already
// succeeded is not disturbed by a retry covering the whole batch.
func (d *Driver) applyPublicParams(ctx context.Context, tmsIDs []token2.TMSID, raw []byte, version uint64) error {
	var errs []error
	for _, tmsID := range tmsIDs {
		if err := d.tmsProvider.Update(tmsID, raw); err != nil {
			logger.Warnf("failed to update tms [%s] to public parameters version %d: %v", tmsID, version, err)
			errs = append(errs, errors.Wrapf(err, "tms [%s]", tmsID))

			continue
		}
		if d.tokensManager == nil {
			continue
		}
		service, err := d.tokensManager.ServiceByTMSId(tmsID)
		if err != nil {
			logger.Warnf("failed to get the token store for [%s]: %v", tmsID, err)
			errs = append(errs, errors.Wrapf(err, "tms [%s]", tmsID))

			continue
		}
		if err := service.StorePublicParams(ctx, raw); err != nil {
			logger.Warnf("failed to store public parameters for [%s]: %v", tmsID, err)
			errs = append(errs, errors.Wrapf(err, "tms [%s]", tmsID))
		}
	}

	return errors.Join(errs...)
}

// installEndorsement builds the endorsement seam for this network and hands it to the network. The
// service itself is per TMS, because it needs that TMS's validator, so the factory is given every
// TMS's own registry, threshold, domain and TokenState (Register) instead of one set shared by the
// whole network, and what is installed on the network is a resolver that asks the factory for the
// right one when it is given a TMS to approve for.
func (d *Driver) installEndorsement(n *Network, namespaces []NamespaceConfig, evmClient client.EVMClient, network, channel string) error {
	if d.viewManager == nil {
		logger.Debugf("no view manager available; this node cannot collect endorsements")

		return nil
	}

	factory, err := endorsement.NewServiceFactory(endorsement.FactoryConfig{
		Client:      evmClient,
		ViewManager: d.viewManager,
	})
	if err != nil {
		return err
	}

	// endorserConfig is the configuration that governs this node's own role as an endorser, which is
	// registered once for the whole node process (see registerEndorser's doc comment). It defaults to
	// the first TMS's configuration, matching this driver's pre-existing choice of "the first TMS
	// declaring the network" as the network-wide representative; if any TMS actually enables
	// endorsing, that TMS's configuration takes over instead, and every other TMS that also enables it
	// must name the same key, since one node signs endorsements with one key for the whole network.
	endorserConfig := namespaces[0].Config
	for _, nc := range namespaces {
		registry, err := nc.Config.EndorserRegistry(d.resolveIdentity)
		if err != nil {
			return errors.Wrapf(err, "evm: tms [%s]", nc.Namespace)
		}
		tokenState, err := nc.Config.TokenStateAddress()
		if err != nil {
			return errors.Wrapf(err, "evm: tms [%s]", nc.Namespace)
		}
		tmsID := token2.TMSID{Network: network, Channel: channel, Namespace: nc.Namespace}
		if err := factory.Register(tmsID, endorsement.TMSConfig{
			Registry:     registry,
			Threshold:    int(nc.Config.Endorsement.Threshold),
			Domain:       eip712.Domain{ChainID: nc.Config.ChainIDBig(), VerifyingContract: tokenState},
			TokenState:   tokenState,
			BlockTag:     nc.Config.Finality.BlockTag,
			PublicParams: pp.NewChainProvider(evmClient, tokenState, nc.Config.Finality.BlockTag),
		}); err != nil {
			return errors.Wrapf(err, "evm: tms [%s]", nc.Namespace)
		}

		if nc.Config.Endorser.Enabled {
			if !endorserConfig.Endorser.Enabled {
				endorserConfig = nc.Config
			} else if endorserConfig.Endorser.Keystore != nc.Config.Endorser.Keystore ||
				endorserConfig.Endorser.Address != nc.Config.Endorser.Address {
				return errors.Errorf(
					"evm: tms [%s] configures this node as an endorser with a different key than "+
						"another tms on [%s:%s]; one node endorses with one key for the whole network",
					nc.Namespace, network, channel)
			}
		}
	}

	n.SetEndorsementFactory(func(tms *token2.ManagementService) (EndorsementService, error) {
		if tms == nil {
			return nil, errors.New("evm: nil token management service")
		}

		// Only the id is kept. The factory resolves the service again on every request, because this
		// one is evicted and rebuilt whenever public parameters change.
		return factory.ForTMS(tms.ID())
	})
	// SetupPublicParams goes through this instead: it may run before any management service exists for
	// the TMS (first-time setup), so it only ever has the id, never the wrapper above requires.
	n.SetEndorsementFactoryByID(func(tmsID token2.TMSID) (EndorsementService, error) {
		return factory.ForTMS(tmsID)
	})

	// Registration happens now, not on the first approval. An endorser node answers requests without
	// ever making one, so registering lazily on the approval path would mean it never registers at
	// all and every request to it times out.
	if err := d.registerEndorser(network+":"+channel, factory, endorserConfig); err != nil {
		return errors.Wrap(err, "evm: failed to register this node as an endorser")
	}

	return nil
}

// newSubmitter builds the account that signs and pays for transactions. A network with no submitter
// configured can still serve reads and collect endorsements, so a missing key is not fatal here; it
// surfaces on the first Broadcast instead, where it is actionable.
func (d *Driver) newSubmitter(config *Config, evmClient client.EVMClient) (*Submitter, error) {
	if strings.TrimSpace(config.Submitter.Keystore) == "" {
		logger.Debugf("no submitter key configured; this node cannot broadcast")

		return nil, nil
	}
	key, err := LoadKeyForAddress(config.Submitter.Keystore, config.Submitter.Address)
	if err != nil {
		return nil, err
	}
	tokenState, err := config.TokenStateAddress()
	if err != nil {
		return nil, err
	}

	return NewSubmitter(evmClient, key, tokenState, config.ChainIDBig(), config.Gas)
}

// registerEndorser registers this node's responder so it can answer requests. A node that does not
// endorse has no key and registers nothing.
//
// The registration is per node, not per network, because FSC routes an incoming session to a
// responder by the initiating view's Go type alone: it has no notion of "this responder, but only for
// network X". So only one Responder can ever be registered for endorsement.Initiator across this
// process's whole lifetime, and whichever network happens to build it first has its factory, EIP-712
// domain and chain client baked into it permanently. A second, differently configured network trying
// to endorse through the same registration would validate against the right TMS but sign and read
// against the wrong chain, so it is refused loudly here instead of silently discarded: an operator who
// configures two endorsing networks on one node needs to see why the second one never answers.
//
// Because a broken endorser configuration answers no requests and looks identical to network trouble
// from the outside, discoverable only once a quorum times out, every failure past that check is
// returned to the caller instead of only logged: a node explicitly configured with endorser.enabled
// must not start up looking healthy while unable to fulfil that role, the same contract newSubmitter
// already holds for a configured-but-broken submitter key. registeredFor is set only once every step
// succeeds, so a failed attempt does not permanently lock the node out of registering on a later,
// successful call for the same network.
//
// The TMS is resolved when a request arrives rather than now: resolving one here would ask the token
// layer for a service that is still being built through this very driver.
func (d *Driver) registerEndorser(networkKey string, factory *endorsement.ServiceFactory, config *Config) error {
	if d.viewRegistry == nil || !config.Endorser.Enabled {
		return nil
	}

	d.registerMu.Lock()
	defer d.registerMu.Unlock()
	if d.registeredFor != "" {
		if d.registeredFor != networkKey {
			logger.Errorf(
				"[%s] is configured to endorse, but this node is already registered as the endorser for [%s]; "+
					"one node can endorse for only one EVM network at a time, [%s] will not answer endorsement requests",
				networkKey, d.registeredFor, networkKey)
		}

		return nil
	}

	signer, err := config.EndorserSigner()
	if err != nil {
		return errors.Wrap(err, "this node is configured as an endorser but its key is unusable")
	}
	// Separately from the error: errors.Wrap(nil, ...) is nil, so folding this into the branch above
	// would report success and leave the node unregistered, which is the exact silent-endorser failure
	// the comment above says must not happen.
	if signer == nil {
		return errors.New("this node is configured as an endorser but no signing key was loaded")
	}
	allowed, err := config.AllowedRequesters(d.resolveIdentity)
	if err != nil {
		return errors.Wrap(err, "failed to resolve the endorsement allowlist")
	}
	authorizer, err := endorsement.NewAuthorizer(allowed)
	if err != nil {
		return errors.Wrap(err, "failed to build the endorsement allowlist")
	}
	responder, err := factory.NewResponder(authorizer, signer, d.resolveTMS)
	if err != nil {
		return errors.Wrap(err, "failed to build the endorsement responder")
	}
	if err := endorsement.RegisterEndorser(d.viewRegistry, responder); err != nil {
		return errors.Wrap(err, "failed to register the endorsement responder")
	}
	d.registeredFor = networkKey
	logger.Infof("registered as the endorser for [%s] with address %s", networkKey, signer.Address())

	return nil
}

// resolveIdentity turns a configured node name into the identity that node speaks with.
func (d *Driver) resolveIdentity(name string) (view2.Identity, error) {
	if d.identities == nil {
		return nil, errors.New("evm: no identity provider available")
	}

	identity := d.identities.Identity(name)
	if identity.IsNone() {
		return nil, errors.Errorf("evm: no identity known for [%s]", name)
	}

	return identity, nil
}

// resolveTMS looks up the management service for a TMS id, for a request that has just arrived.
func (d *Driver) resolveTMS(tmsID token2.TMSID) (*token2.ManagementService, error) {
	if d.tmsProvider == nil {
		return nil, errors.New("evm: no token management service provider available")
	}

	return d.tmsProvider.GetManagementService(token2.WithTMSID(tmsID))
}

// configNetworkResolver resolves EVM networks from the token-sdk configuration.
type configNetworkResolver struct {
	cs *config.Service
}

// IsEVMNetwork reports whether any configured TMS for the given network/channel declares the EVM
// network service block.
func (r *configNetworkResolver) IsEVMNetwork(network, channel string) bool {
	configs, err := r.cs.Configurations()
	if err != nil {
		logger.Errorf("failed to load token-sdk configurations while resolving evm network [%s:%s]: %v", network, channel, err)

		return false
	}
	for _, c := range configs {
		id := c.ID()
		if id.Network == network && id.Channel == channel && c.IsSet(EVMConfigKey) {
			return true
		}
	}

	return false
}

// TMSIDsFor returns the id of every TMS declaring the EVM network service on the given
// network/channel.
func (r *configNetworkResolver) TMSIDsFor(network, channel string) []token2.TMSID {
	configs, err := r.cs.Configurations()
	if err != nil {
		logger.Errorf("failed to load token-sdk configurations while listing tms for [%s:%s]: %v", network, channel, err)

		return nil
	}
	var out []token2.TMSID
	for _, c := range configs {
		id := c.ID()
		if id.Network == network && id.Channel == channel && c.IsSet(EVMConfigKey) {
			// token.TMSID is an alias for the driver's, so the configuration's id is already the right type.
			out = append(out, id)
		}
	}

	return out
}

// ConfigFor loads and validates the EVM configuration of the first TMS declaring it for the given
// network/channel. Every TMS on one EVM network shares the endpoint and chain, so the first match is
// the network's configuration.
func (r *configNetworkResolver) ConfigFor(network, channel string) (*Config, error) {
	configs, err := r.cs.Configurations()
	if err != nil {
		return nil, errors.Wrapf(err, "evm: failed to load token-sdk configurations for [%s:%s]", network, channel)
	}
	for _, c := range configs {
		id := c.ID()
		if id.Network == network && id.Channel == channel && c.IsSet(EVMConfigKey) {
			return LoadConfig(c)
		}
	}

	return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
}

// ConfigurationFor returns the token-sdk configuration of one TMS.
func (r *configNetworkResolver) ConfigurationFor(tmsID token2.TMSID) (*config.Configuration, error) {
	return r.cs.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
}

// ConfigForTMS loads and validates one TMS's own EVM configuration, from its own services.network.evm
// block.
func (r *configNetworkResolver) ConfigForTMS(tmsID token2.TMSID) (*Config, error) {
	c, err := r.cs.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, errors.Wrapf(err, "evm: failed to load the token-sdk configuration for [%s]", tmsID)
	}
	if !c.IsSet(EVMConfigKey) {
		return nil, errors.Errorf("evm: tms [%s] has no evm network configuration", tmsID)
	}

	return LoadConfig(c)
}

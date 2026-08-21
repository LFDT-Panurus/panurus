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
	// ConfigFor returns the EVM configuration for the given network/channel.
	ConfigFor(network, channel string) (*Config, error)
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
	// watchers keeps one public-parameters watcher per network, so building a network twice does not
	// leave two pollers on one contract.
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
// It builds what is network-scoped: the client, and the submitter from the configured key. The
// endorsement service is per-TMS, because it needs that TMS's validator, so it is supplied separately
// by the SDK wiring; likewise local membership, whose identities the driver does not own.
func (d *Driver) New(network, channel string) (driver.Network, error) {
	if !d.resolver.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
	}
	logger.Debugf("creating evm network [%s:%s]", network, channel)

	config, err := d.resolver.ConfigFor(network, channel)
	if err != nil {
		return nil, err
	}
	evmClient, err := client.NewJSONRPCClient(config.Endpoint, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "evm: failed to create a client for [%s:%s]", network, channel)
	}

	submitter, err := d.newSubmitter(config, evmClient)
	if err != nil {
		return nil, err
	}

	n, err := NewNetwork(network, config, evmClient, nil, submitter, d.membership)
	if err != nil {
		return nil, err
	}
	if err := d.installEndorsement(n, config, evmClient, network, channel); err != nil {
		return nil, err
	}
	d.watchPublicParams(network, channel, config, evmClient)

	// Recovery starts when a namespace binds to the network rather than here, because it is per TMS
	// and the namespace is only known then. Mirrors the Fabric driver, which starts it in connect.
	n.SetRecoveryStarter(func(ns string) error {
		return d.startRecovery(token2.TMSID{Network: network, Channel: channel, Namespace: ns}, n)
	})

	return n, nil
}

// watchPublicParams starts watching the chain for public-parameters updates on this network.
//
// Parameters change through an endorsed setup delta that some other node submits, so there is nothing
// local to trigger off: without this a node keeps serving whatever it started with. Fabric gets the
// same signal from a listener on the setup key.
func (d *Driver) watchPublicParams(network, channel string, config *Config, evmClient client.EVMClient) {
	if d.tmsProvider == nil {
		logger.Debugf("no token management service provider; this node cannot reload public parameters")

		return
	}

	key := network + ":" + channel
	d.watchersMu.Lock()
	defer d.watchersMu.Unlock()
	if _, running := d.watchers[key]; running {
		return
	}

	tokenState, err := config.TokenStateAddress()
	if err != nil {
		logger.Errorf("cannot watch public parameters for [%s]: %v", key, err)

		return
	}
	tmsIDs := d.resolver.TMSIDsFor(network, channel)
	if len(tmsIDs) == 0 {
		return
	}

	watcher, err := pp.NewWatcher(
		evmClient, tokenState, config.Finality.BlockTag, config.Finality.PollInterval,
		func(ctx context.Context, raw []byte, version uint64) error {
			return d.applyPublicParams(ctx, tmsIDs, raw, version)
		},
	)
	if err != nil {
		logger.Errorf("cannot watch public parameters for [%s]: %v", key, err)

		return
	}
	watcher.Start(context.Background())
	d.watchers[key] = watcher
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
// service itself is per TMS, because it needs that TMS's validator, so what is installed is a factory
// that resolves one when the network is given a TMS to approve for.
func (d *Driver) installEndorsement(n *Network, config *Config, evmClient client.EVMClient, network, channel string) error {
	if d.viewManager == nil {
		logger.Debugf("no view manager available; this node cannot collect endorsements")

		return nil
	}

	registry, err := config.EndorserRegistry(d.resolveIdentity)
	if err != nil {
		return err
	}
	tokenState, err := config.TokenStateAddress()
	if err != nil {
		return err
	}

	factory, err := endorsement.NewServiceFactory(endorsement.FactoryConfig{
		Registry:     registry,
		Threshold:    int(config.Endorsement.Threshold),
		Domain:       eip712.Domain{ChainID: config.ChainIDBig(), VerifyingContract: tokenState},
		Client:       evmClient,
		TokenState:   tokenState,
		BlockTag:     config.Finality.BlockTag,
		PublicParams: pp.NewChainProvider(evmClient, tokenState, config.Finality.BlockTag),
		ViewManager:  d.viewManager,
		TMS:          d.resolveTMS,
	})
	if err != nil {
		return err
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
	d.registerEndorser(network+":"+channel, factory, config)

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
// The TMS is resolved when a request arrives rather than now: resolving one here would ask the token
// layer for a service that is still being built through this very driver.
func (d *Driver) registerEndorser(networkKey string, factory *endorsement.ServiceFactory, config *Config) {
	if d.viewRegistry == nil || !config.Endorser.Enabled {
		return
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

		return
	}

	signer, err := config.EndorserSigner()
	if err != nil || signer == nil {
		logger.Errorf("this node is configured as an endorser but its key is unusable: %v", err)

		return
	}
	allowed, err := config.AllowedRequesters(d.resolveIdentity)
	if err != nil {
		logger.Errorf("failed to resolve the endorsement allowlist: %v", err)

		return
	}
	authorizer, err := endorsement.NewAuthorizer(allowed)
	if err != nil {
		logger.Errorf("failed to build the endorsement allowlist: %v", err)

		return
	}
	responder, err := factory.NewResponder(authorizer, signer, d.resolveTMS)
	if err != nil {
		logger.Errorf("failed to build the endorsement responder: %v", err)

		return
	}
	if err := endorsement.RegisterEndorser(d.viewRegistry, responder); err != nil {
		logger.Errorf("failed to register the endorsement responder: %v", err)

		return
	}
	d.registeredFor = networkKey
	logger.Infof("registered as the endorser for [%s] with address %s", networkKey, signer.Address())
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

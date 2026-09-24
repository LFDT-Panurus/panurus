/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"sync"
	"testing"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	tokendriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	networkpkg "github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	tokendbmock "github.com/LFDT-Panurus/panurus/token/services/storage/tokendb/mock"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/pp"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events"
	svcview "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// fakeResolver reports a fixed set of (network|channel) pairs as EVM networks and yields a minimal
// valid configuration for them.
type fakeResolver struct {
	evm map[string]bool
}

func (f fakeResolver) IsEVMNetwork(network, channel string) bool {
	return f.evm[network+"|"+channel]
}

func (f fakeResolver) ConfigFor(network, channel string) (*Config, error) {
	if !f.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("no evm configuration for [%s:%s]", network, channel)
	}
	c := validConfig()
	c.applyDefaults()

	return c, c.Validate()
}

// ConfigForTMS mirrors ConfigFor: the routing tests only ever exercise a single TMS, so both return
// the same fixture.
func (f fakeResolver) ConfigForTMS(tmsID token2.TMSID) (*Config, error) {
	return f.ConfigFor(tmsID.Network, tmsID.Channel)
}

// TMSIDsFor reports one TMS per configured network, enough for the routing tests: they never exercise
// the public-parameters watcher, which needs a TMS provider the fake does not have.
func (f fakeResolver) TMSIDsFor(network, channel string) []token2.TMSID {
	if !f.IsEVMNetwork(network, channel) {
		return nil
	}

	return []token2.TMSID{{Network: network, Channel: channel, Namespace: "token"}}
}

// ConfigurationFor reports no configuration: the routing tests have no config service, and every
// caller of this treats a missing configuration as "use the defaults".
func (f fakeResolver) ConfigurationFor(tmsID token2.TMSID) (*config.Configuration, error) {
	return nil, errors.Errorf("no configuration for [%s]", tmsID)
}

func TestDriverNewRouting(t *testing.T) {
	d := &Driver{resolver: fakeResolver{evm: map[string]bool{"evm-net|": true}}}

	// An EVM-configured network yields a Network.
	n, err := d.New("evm-net", "")
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, "evm-net", n.Name())
	assert.Empty(t, n.Channel())

	// A non-EVM network must error so the provider falls through to the next driver.
	_, err = d.New("fabric-net", "")
	require.Error(t, err)

	// The right network name but a mismatched channel must also fall through.
	_, err = d.New("evm-net", "other-channel")
	assert.Error(t, err)
}

// TestNetworkSurfaceIsWired checks the methods that used to be stubs now answer through the finality
// manager rather than returning a not-implemented error.
func TestNetworkSurfaceIsWired(t *testing.T) {
	evm := &mock.EVMClient{}
	// getTokenRequestHash returns the zero hash: the anchor has not been applied.
	evm.CallReturns(make([]byte, 32), nil)
	n := testNetwork(t, evm, nil)

	assert.NotNil(t, n.NewEnvelope())

	ledger, err := n.Ledger()
	require.NoError(t, err)
	require.NotNil(t, ledger)

	// An anchor the chain has never seen is Unknown: never the invalid zero code, and never Invalid,
	// because a reverted apply is indistinguishable from one still pending (design 7.4).
	status, hash, _, err := n.GetTransactionStatus(t.Context(), "token", anchorHex(0x01))
	require.NoError(t, err)
	assert.Equal(t, driver.Unknown, status)
	assert.Nil(t, hash)

	code, err := ledger.Status(anchorHex(0x01))
	require.NoError(t, err)
	assert.Equal(t, driver.Unknown, code)
}

// TestNetworkRejectsMalformedTransactionID checks the anchor-shaped identifier is validated rather
// than silently producing a wrong on-chain lookup.
func TestNetworkRejectsMalformedTransactionID(t *testing.T) {
	n := testNetwork(t, nil, nil)

	_, _, _, err := n.GetTransactionStatus(t.Context(), "token", "not-a-valid-anchor")
	require.Error(t, err)

	err = n.AddFinalityListener("token", "not-a-valid-anchor", nil)
	require.Error(t, err)
}

// --- registerEndorser ------------------------------------------------------------------------------

// fakeViewRegistry records every RegisterResponder call, so a test can tell whether a second
// registration attempt actually reached FSC or was refused before getting there.
type fakeViewRegistry struct {
	calls int
}

func (f *fakeViewRegistry) RegisterResponder(view.View, any) error {
	f.calls++

	return nil
}

// fakeViewManager satisfies endorsement.ViewManager without ever running a view; registerEndorser
// itself never calls InitiateView, only Service.Endorse does.
type fakeViewManager struct{}

func (fakeViewManager) InitiateView(context.Context, view.View) (any, error) { return nil, nil }

// fakePPValidator satisfies endorsement.PublicParamsValidator without a real token driver; these tests
// never actually endorse a setup request, only build the factory that would serve one.
type fakePPValidator struct{}

func (fakePPValidator) PublicParametersFromBytes([]byte) (tokendriver.PublicParameters, error) {
	return nil, errors.New("fakePPValidator: not implemented")
}

// fakeIdentityProvider resolves every name to an identity carrying the same bytes, so
// config.AllowedRequesters can turn the allowlist's names into identities the way d.resolveIdentity
// does in production.
type fakeIdentityProvider struct{}

func (fakeIdentityProvider) Identity(name string) view.Identity { return view.Identity(name) }
func (fakeIdentityProvider) DefaultIdentity() view.Identity     { return view.Identity("default") }

var _ svcview.IdentityProvider = fakeIdentityProvider{}

// endorserConfig returns a configuration for a node that endorses, backed by the well-known test key
// keystore_test.go already writes, whose address matches validConfig's single endorser entry.
func endorserConfig(t *testing.T) *Config {
	t.Helper()
	c := validConfig()
	c.Endorser = EndorserConfig{
		Enabled:  true,
		Keystore: writeKey(t, testKeyHex),
		Address:  testKeyAddress,
	}
	c.Endorsement.Allowlist = []string{"endorser-1"}

	return c
}

// testServiceFactory builds a real *endorsement.ServiceFactory over config, the same shape
// installEndorsement assembles, so registerEndorser is exercised with the collaborator it actually
// gets in production. It registers config under a single fixed TMS id, which is all these tests need:
// registerEndorser never resolves a TMS itself, it only builds the responder factory.NewResponder
// hands back.
func testServiceFactory(t *testing.T, config *Config) *endorsement.ServiceFactory {
	t.Helper()
	registry, err := config.EndorserRegistry(func(name string) (view.Identity, error) {
		return view.Identity(name), nil
	})
	require.NoError(t, err)
	tokenState, err := config.TokenStateAddress()
	require.NoError(t, err)
	evmClient := &mock.EVMClient{}

	factory, err := endorsement.NewServiceFactory(endorsement.FactoryConfig{
		Client:      evmClient,
		ViewManager: fakeViewManager{},
		PPValidator: fakePPValidator{},
	})
	require.NoError(t, err)
	require.NoError(t, factory.Register(token2.TMSID{Network: "evm", Namespace: "token"}, endorsement.TMSConfig{
		Registry:     registry,
		Threshold:    int(config.Endorsement.Threshold),
		Domain:       eip712.Domain{ChainID: config.ChainIDBig(), VerifyingContract: tokenState},
		TokenState:   tokenState,
		BlockTag:     config.Finality.BlockTag,
		PublicParams: pp.NewChainProvider(evmClient, tokenState, config.Finality.BlockTag),
	}))

	return factory
}

// TestRegisterEndorserRefusesASecondNetwork is the fix for a real bug. FSC registers a responder by
// the initiating view's Go type alone, so only one Responder can ever be registered for
// endorsement.Initiator across this node's whole process lifetime. Before this fix a sync.Once
// silently discarded every network after the first one that reached registerEndorser, with no signal
// at all that a second, differently configured network's endorsement requests would never be
// answered, or worse, would be answered through the wrong network's chain client and EIP-712 domain.
func TestRegisterEndorserRefusesASecondNetwork(t *testing.T) {
	registry := &fakeViewRegistry{}
	d := &Driver{viewRegistry: registry, identities: fakeIdentityProvider{}}
	config := endorserConfig(t)
	factory := testServiceFactory(t, config)

	require.NoError(t, d.registerEndorser("network-a:", factory, config))
	assert.Equal(t, 1, registry.calls, "the first network must register")
	assert.Equal(t, "network-a:", d.registeredFor)

	require.NoError(t, d.registerEndorser("network-b:", factory, config),
		"a second network is refused loudly via a log line, not an error - see registerEndorser's doc comment")
	assert.Equal(t, 1, registry.calls, "a second, different network must not overwrite the registration")
	assert.Equal(t, "network-a:", d.registeredFor, "the first network's registration must stand")
}

// TestRegisterEndorserIsIdempotentForTheSameNetwork checks Driver.New being called twice for the same
// network (a network rebuilt over a node's life) does not trip the second-network refusal.
func TestRegisterEndorserIsIdempotentForTheSameNetwork(t *testing.T) {
	registry := &fakeViewRegistry{}
	d := &Driver{viewRegistry: registry, identities: fakeIdentityProvider{}}
	config := endorserConfig(t)
	factory := testServiceFactory(t, config)

	require.NoError(t, d.registerEndorser("network-a:", factory, config))
	require.NoError(t, d.registerEndorser("network-a:", factory, config))

	assert.Equal(t, 1, registry.calls, "registering the same network twice must not re-register")
}

// TestRegisterEndorserSkipsANonEndorsingNetwork checks a network that is not configured to endorse
// never touches the registration state, so it cannot trigger the second-network refusal for a network
// that never wanted to endorse in the first place.
func TestRegisterEndorserSkipsANonEndorsingNetwork(t *testing.T) {
	registry := &fakeViewRegistry{}
	d := &Driver{viewRegistry: registry, identities: fakeIdentityProvider{}}
	endorsing := endorserConfig(t)
	factory := testServiceFactory(t, endorsing)
	require.NoError(t, d.registerEndorser("network-a:", factory, endorsing))

	notEndorsing := validConfig() // Endorser.Enabled defaults to false
	require.NoError(t, d.registerEndorser("network-b:", factory, notEndorsing))

	assert.Equal(t, 1, registry.calls)
	assert.Equal(t, "network-a:", d.registeredFor)
}

// TestRegisterEndorserReturnsAnErrorForABrokenKey is the regression test for the finding that a node
// explicitly configured as an endorser, but whose signing key cannot be loaded, used to register
// nothing and only log the failure: nothing told the caller registration never happened, so
// installEndorsement and Driver.New both reported success regardless. A broken endorser answers no
// requests, which looks identical to ordinary network trouble from the outside and was otherwise
// discoverable only once a quorum it was needed for timed out.
func TestRegisterEndorserReturnsAnErrorForABrokenKey(t *testing.T) {
	registry := &fakeViewRegistry{}
	d := &Driver{viewRegistry: registry, identities: fakeIdentityProvider{}}
	config := endorserConfig(t)
	config.Endorser.Keystore = "" // unusable: LoadKey rejects an empty path
	factory := testServiceFactory(t, config)

	err := d.registerEndorser("network-a:", factory, config)
	require.Error(t, err)
	assert.Zero(t, registry.calls, "a broken key must not reach the view registry")
	assert.Empty(t, d.registeredFor, "a failed attempt must not mark the network as registered")
}

// --- NewDriver ---------------------------------------------------------------------------------

// TestNewDriver checks the factory wires every collaborator it is given, rather than leaving any of
// the maps or the resolver nil, which would panic on first use instead of at construction.
func TestNewDriver(t *testing.T) {
	cs := config.NewService(fakeConfigProvider{})
	d := NewDriver(
		cs, fakeIdentityProvider{}, nil, nil, nil, nil, nil, nil,
		tracenoop.NewTracerProvider(), nil, nil,
	)

	impl, ok := d.(*Driver)
	require.True(t, ok)
	assert.NotNil(t, impl.resolver)
	assert.NotNil(t, impl.membership)
	assert.NotNil(t, impl.watchers)
	assert.NotNil(t, impl.recoveries)
}

// fakeConfigProvider satisfies config.Provider with no configuration at all, enough to build a
// config.Service that NewDriver can wrap without reading any real file.
type fakeConfigProvider struct{}

func (fakeConfigProvider) UnmarshalKey(string, any) error                     { return nil }
func (fakeConfigProvider) GetString(string) string                            { return "" }
func (fakeConfigProvider) IsSet(string) bool                                  { return false }
func (fakeConfigProvider) TranslatePath(path string) string                   { return path }
func (fakeConfigProvider) GetBool(string) bool                                { return false }
func (fakeConfigProvider) MergeConfig([]byte) error                           { return nil }
func (fakeConfigProvider) ProvideFromRaw([]byte) (*fscconfig.Provider, error) { return nil, nil }

// --- resolveTMS ----------------------------------------------------------------------------------

func TestResolveTMS(t *testing.T) {
	t.Run("no provider configured is an error", func(t *testing.T) {
		d := &Driver{}
		_, err := d.resolveTMS(testTMSID())
		require.Error(t, err)
	})
}

// --- configNetworkResolver ------------------------------------------------------------------------

// fakeTokenManagerServiceProvider is the driver-level seam token2.ManagementServiceProvider wraps.
// Only Update is exercised by applyPublicParams; the other two methods are never reached in these
// tests.
type fakeTokenManagerServiceProvider struct {
	updateErr map[string]error
}

func (f *fakeTokenManagerServiceProvider) GetTokenManagerService(tokendriver.ServiceOptions) (tokendriver.TokenManagerService, error) {
	return nil, nil
}

func (f *fakeTokenManagerServiceProvider) Update(opts tokendriver.ServiceOptions) error {
	return f.updateErr[opts.Namespace]
}

func (f *fakeTokenManagerServiceProvider) ConfigurationFor(string, string, string) (tokendriver.Configuration, error) {
	return nil, nil
}

// noopPublisher satisfies events.Publisher without doing anything: applyPublicParams's tokens-manager
// path never actually publishes in the scenarios covered here.
type noopPublisher struct{}

func (noopPublisher) Publish(events.Event) {}

// fakeTMSProviderForTokens and fakeNetworkProviderForTokens satisfy tokens.TMSProvider and
// tokens.NetworkProvider respectively; the ServiceManager only needs them to build a *Service lazily,
// which these tests never reach because the store lookup fails first.
type fakeTMSProviderForTokens struct{}

func (fakeTMSProviderForTokens) GetManagementService(...token2.ServiceOption) (*token2.ManagementService, error) {
	return nil, errors.New("not used in this test")
}

type fakeNetworkProviderForTokens struct{}

func (fakeNetworkProviderForTokens) GetNetwork(string, string) (*networkpkg.Network, error) {
	return nil, errors.New("not used in this test")
}

// TestApplyPublicParams exercises the reachable branches without a full token-store stack: a
// per-tms Update failure is collected rather than aborting the batch, a nil tokens manager skips
// the store step entirely, and a failing token store lookup is also collected as an error.
func TestApplyPublicParams(t *testing.T) {
	tmsA := token2.TMSID{Network: "evm-net", Namespace: "a"}
	tmsB := token2.TMSID{Network: "evm-net", Namespace: "b"}

	t.Run("all updates succeed and there is no tokens manager", func(t *testing.T) {
		fakeTMSP := &fakeTokenManagerServiceProvider{updateErr: map[string]error{}}
		d := &Driver{tmsProvider: token2.NewManagementServiceProvider(fakeTMSP, nil, nil, nil, nil)}

		err := d.applyPublicParams(t.Context(), []token2.TMSID{tmsA, tmsB}, []byte("pp"), 1)
		require.NoError(t, err)
	})

	t.Run("a failed update for one tms does not stop the others", func(t *testing.T) {
		fakeTMSP := &fakeTokenManagerServiceProvider{updateErr: map[string]error{
			"a": errors.New("boom"),
		}}
		d := &Driver{tmsProvider: token2.NewManagementServiceProvider(fakeTMSP, nil, nil, nil, nil)}

		err := d.applyPublicParams(t.Context(), []token2.TMSID{tmsA, tmsB}, []byte("pp"), 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})

	t.Run("a failing token store lookup is reported", func(t *testing.T) {
		fakeTMSP := &fakeTokenManagerServiceProvider{updateErr: map[string]error{}}
		storeManager := &tokendbmock.TokenStoreServiceManager{}
		storeManager.StoreServiceByTMSIdReturns(nil, errors.New("no store"))
		tokensManager := tokens.NewServiceManager(
			fakeTMSProviderForTokens{}, storeManager, fakeNetworkProviderForTokens{}, noopPublisher{},
		)
		d := &Driver{
			tmsProvider:   token2.NewManagementServiceProvider(fakeTMSP, nil, nil, nil, nil),
			tokensManager: tokensManager,
		}

		err := d.applyPublicParams(t.Context(), []token2.TMSID{tmsA}, []byte("pp"), 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no store")
	})
}

// --- configNetworkResolver, backed by a real config.Service -----------------------------------------

// evmConfigServiceFixture builds a config.Service over the yaml fixture under testdata/config,
// which declares one TMS with an evm network block and one without, on the same network/channel plus
// a second, unrelated network.
func evmConfigServiceFixture(t *testing.T) *config.Service {
	t.Helper()
	cp, err := fscconfig.NewProvider("./testdata/config")
	require.NoError(t, err)

	return config.NewService(cp)
}

func TestConfigNetworkResolver(t *testing.T) {
	cs := evmConfigServiceFixture(t)
	r := &configNetworkResolver{cs: cs}

	t.Run("IsEVMNetwork", func(t *testing.T) {
		assert.True(t, r.IsEVMNetwork("evm-net", "chan1"), "a tms declaring services.network.evm must be found")
		assert.False(t, r.IsEVMNetwork("fabric-net", "chanF"), "a tms with no evm block must not be reported")
		assert.False(t, r.IsEVMNetwork("no-such-network", ""))
	})

	t.Run("TMSIDsFor", func(t *testing.T) {
		ids := r.TMSIDsFor("evm-net", "chan1")
		require.Len(t, ids, 1)
		assert.Equal(t, "ns1", ids[0].Namespace)

		assert.Empty(t, r.TMSIDsFor("fabric-net", "chanF"))
	})

	t.Run("ConfigFor loads the first tms declaring the network", func(t *testing.T) {
		cfg, err := r.ConfigFor("evm-net", "chan1")
		require.NoError(t, err)
		assert.Equal(t, int64(testChainID), cfg.ChainID)
	})

	t.Run("ConfigFor errors for a network with no evm tms", func(t *testing.T) {
		_, err := r.ConfigFor("fabric-net", "chanF")
		require.Error(t, err)
	})

	t.Run("ConfigForTMS loads the tms's own configuration", func(t *testing.T) {
		cfg, err := r.ConfigForTMS(token2.TMSID{Network: "evm-net", Channel: "chan1", Namespace: "ns1"})
		require.NoError(t, err)
		assert.Equal(t, int64(testChainID), cfg.ChainID)
	})

	t.Run("ConfigForTMS errors for a tms with no evm block", func(t *testing.T) {
		_, err := r.ConfigForTMS(token2.TMSID{Network: "fabric-net", Channel: "chanF", Namespace: "ns2"})
		require.Error(t, err)
	})

	t.Run("ConfigForTMS errors for an unknown tms", func(t *testing.T) {
		_, err := r.ConfigForTMS(token2.TMSID{Network: "no-such", Channel: "", Namespace: "none"})
		require.Error(t, err)
	})

	t.Run("ConfigurationFor returns the raw tms configuration", func(t *testing.T) {
		cfg, err := r.ConfigurationFor(token2.TMSID{Network: "evm-net", Channel: "chan1", Namespace: "ns1"})
		require.NoError(t, err)
		assert.True(t, cfg.IsSet(EVMConfigKey))
	})

	t.Run("ConfigurationFor errors for an unknown tms", func(t *testing.T) {
		_, err := r.ConfigurationFor(token2.TMSID{Network: "no-such", Channel: "", Namespace: "none"})
		require.Error(t, err)
	})
}

// --- watchPublicParams -----------------------------------------------------------------------------

// TestWatchPublicParamsWithoutTMSProvider checks the early return: a node with no way to reload a
// TMS's parameters must not try to build a watcher at all.
func TestWatchPublicParamsWithoutTMSProvider(t *testing.T) {
	d := &Driver{watchers: map[string]*pp.Watcher{}}
	c := validConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())

	d.watchPublicParams("evm-net", "", []NamespaceConfig{{Namespace: "token", Config: c}}, &mock.EVMClient{})

	assert.Empty(t, d.watchers, "no watcher may be started without a tms provider")
}

// TestWatchPublicParamsReadsAtLatestNotFinality is the regression test for issue #2412 item 2: the
// watcher used to poll at cfg.Finality.BlockTag (finalized by default), while the endorsement path's
// own ChainProvider deliberately reads the same contract at latest to match what
// TokenState.applyStateDelta checks at apply time. That mismatch meant a node's local view of public
// parameters lagged an already-mined setup update by the whole finalization window, during which every
// ordinary approval this node endorses was refused with ErrStalePublicParams. The watcher must read at
// latest regardless of what Finality.BlockTag is configured to.
func TestWatchPublicParamsReadsAtLatestNotFinality(t *testing.T) {
	c := validConfig()
	c.applyDefaults()
	c.Finality.BlockTag = client.BlockTagFinalized
	c.Finality.PollInterval = 5 * time.Millisecond
	require.NoError(t, c.Validate())

	d := &Driver{
		watchers:    map[string]*pp.Watcher{},
		tmsProvider: token2.NewManagementServiceProvider(&fakeTokenManagerServiceProvider{updateErr: map[string]error{}}, nil, nil, nil, nil),
	}

	var mu sync.Mutex
	var tags []string
	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, _ []byte, blockTag string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		tags = append(tags, blockTag)

		return nil, nil
	}

	d.watchPublicParams("evm-net", "", []NamespaceConfig{{Namespace: "token", Config: c}}, evmClient)
	t.Cleanup(func() {
		for _, w := range d.watchers {
			w.Stop()
		}
	})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(tags) > 0
	}, time.Second, 5*time.Millisecond, "the watcher must poll the chain at least once")

	mu.Lock()
	defer mu.Unlock()
	for _, tag := range tags {
		assert.Equal(t, client.BlockTagLatest, tag,
			"the watcher must read at latest even though Finality.BlockTag is configured to finalized")
	}
}

// TestWatchPublicParamsSkipsABadTokenState checks a namespace whose TokenStateAddress cannot be
// parsed is logged and skipped rather than starting a watcher on a garbage address or panicking.
func TestWatchPublicParamsSkipsABadTokenState(t *testing.T) {
	d := &Driver{
		watchers:    map[string]*pp.Watcher{},
		tmsProvider: token2.NewManagementServiceProvider(&fakeTokenManagerServiceProvider{updateErr: map[string]error{}}, nil, nil, nil, nil),
	}
	bad := validConfig()
	bad.applyDefaults()
	bad.Contracts.TokenState = "not-an-address"

	d.watchPublicParams("evm-net", "", []NamespaceConfig{{Namespace: "token", Config: bad}}, &mock.EVMClient{})

	assert.Empty(t, d.watchers, "a namespace with an unparsable token state must not start a watcher")
}

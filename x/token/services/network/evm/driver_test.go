/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/pp"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	svcview "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

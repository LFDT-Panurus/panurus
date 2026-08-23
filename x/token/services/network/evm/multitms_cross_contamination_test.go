/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

// Round 4, target #4: multi-TMS cross-contamination, built and observed live.
//
// This is the regression test for deferred finding #3 (BUG_HUNT_PROMPT.md's "known, deliberately
// deferred" list): configNetworkResolver.ConfigFor (driver.go:479-492) picks one *Config for the
// first TMS to declare an EVM network/channel, and token/services/network/network.go's Provider
// memoizes one *Network per (network, channel) with the namespace NOT part of the memoization key
// (network.go:344-357, 365, 428-448 in the repo root module). Every other TMS sharing that
// network/channel is therefore routed, silently, through the first TMS's Contracts.TokenState,
// EndorsementVerifier/EIP-712 domain, Submitter and Gas policy.
//
// KEEP THIS TEST. It is not a throwaway: it is meant to start failing (RED) the moment the
// eventual fix splits Config into network-shared vs. per-TMS parts, at which point it should be
// updated to assert isolation instead of contamination.

import (
	"math/big"
	"reflect"
	"testing"
	"unsafe"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
)

// crossTMSResolver simulates configNetworkResolver's real behavior (driver.go:431-497) for two TMS
// sharing one network/channel, each carrying its own EVM configuration with a distinct TokenState.
//
// ConfigFor mirrors configNetworkResolver.ConfigFor's loop exactly: it walks the TMS in declared
// order and returns the FIRST one's *Config for the network/channel -- there is no way for it to
// take a TMS/namespace as input at all, which is the root of the bug. configForCalls counts how many
// times it was asked, so the test can prove Driver.New resolves configuration exactly once for the
// pair, not once per TMS.
type crossTMSResolver struct {
	network, channel string
	// order lists the TMS ids in the order configNetworkResolver.ConfigFor would encounter them
	// while walking config.Service.Configurations(); order[0] is "the first TMS to declare it".
	order          []token2.TMSID
	configs        map[token2.TMSID]*Config
	configForCalls int
}

func (r *crossTMSResolver) IsEVMNetwork(network, channel string) bool {
	return network == r.network && channel == r.channel
}

func (r *crossTMSResolver) TMSIDsFor(network, channel string) []token2.TMSID {
	if !r.IsEVMNetwork(network, channel) {
		return nil
	}

	return append([]token2.TMSID(nil), r.order...)
}

func (r *crossTMSResolver) ConfigFor(network, channel string) (*Config, error) {
	r.configForCalls++
	if !r.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("no evm configuration for [%s:%s]", network, channel)
	}

	return r.configs[r.order[0]], nil
}

func (r *crossTMSResolver) ConfigurationFor(tmsID token2.TMSID) (*config.Configuration, error) {
	return nil, errors.Errorf("no configuration for [%s]", tmsID)
}

// driverNewWithClient reproduces Driver.New's body (driver.go:144-180) line for line, with exactly
// one substitution: the real client.NewJSONRPCClient(config.Endpoint, nil) call is replaced with an
// already-constructed client.EVMClient (a *mock.EVMClient in this test).
//
// New has no seam to inject a client -- it always dials config.Endpoint for real -- and this test
// needs to observe every chain-facing call the constructed Network, Submitter and endorsement
// factory make, which a live TCP dial cannot give it. Every other line -- resolving configuration
// through the resolver, newSubmitter, NewNetwork, installEndorsement, watchPublicParams, the
// recovery-starter wiring -- is copied unchanged from New, and it is exactly that part the test
// exercises; only the transport, which the cross-TMS routing bug has nothing to do with, differs.
func driverNewWithClient(d *Driver, network, channel string, evmClient client.EVMClient) (*Network, error) {
	if !d.resolver.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
	}
	config, err := d.resolver.ConfigFor(network, channel)
	if err != nil {
		return nil, err
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
	n.SetRecoveryStarter(func(ns string) error {
		return d.startRecovery(token2.TMSID{Network: network, Channel: channel, Namespace: ns}, n)
	})

	return n, nil
}

// extractDomain reads the live, unexported eip712.Domain field off a *endorsement.Service returned
// through the EndorsementService interface, via reflection. This is test-only introspection -- no
// production code is touched or needs to be -- used because endorsement.Service.domain has no
// exported accessor and the point of this test is to observe the actual object the driver built for
// TMS B, not to re-derive what it "should" contain.
func extractDomain(t *testing.T, svc EndorsementService) eip712.Domain {
	t.Helper()
	v := reflect.ValueOf(svc)
	require.Equal(t, reflect.Pointer, v.Kind(), "expected a pointer-backed *endorsement.Service")
	v = v.Elem()
	f := v.FieldByName("domain")
	require.True(t, f.IsValid(), "endorsement.Service is expected to carry an unexported 'domain' field")
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	domain, ok := f.Interface().(eip712.Domain)
	require.True(t, ok, "domain field was not an eip712.Domain")

	return domain
}

// TestMultiTMSCrossContamination_Live builds two TMS on one EVM network/channel, each with its own,
// different TokenState clone, drives them through the actual production entry points (Driver.New
// once, Network.Connect per TMS -- confirmed below by call-counting rather than assumed), and
// observes -- live, on the constructed objects, not by re-reading source -- which TokenState address
// ends up backing TMS B's reads, its submitter, and its EIP-712 signing domain.
func TestMultiTMSCrossContamination_Live(t *testing.T) {
	const network = "evm-net"
	const channel = ""

	tmsA := token2.TMSID{Network: network, Channel: channel, Namespace: "tms-a"}
	tmsB := token2.TMSID{Network: network, Channel: channel, Namespace: "tms-b"}

	// Two TMS, two genuinely different TokenState clones. If Config were split per TMS the way the
	// eventual fix needs to, these would never be conflated.
	addrA, err := client.HexToAddress("0x" + repeat("aa", 20))
	require.NoError(t, err)
	addrB, err := client.HexToAddress("0x" + repeat("bb", 20))
	require.NoError(t, err)

	configA := validConfig()
	configA.Contracts.TokenState = addrA.Hex()
	configA.Submitter = SubmitterConfig{Keystore: writeKey(t, testKeyHex), Address: testKeyAddress}
	configA.applyDefaults()
	require.NoError(t, configA.Validate())

	configB := validConfig()
	configB.Contracts.TokenState = addrB.Hex()
	// Give B a materially different endorsement policy too (a config for a totally independent
	// deployment, not a typo of A's), so nothing about this test depends on A and B being
	// accidentally similar.
	configB.Endorsement.Endorsers = []EndorserBinding{
		{Address: "0x9e5f4552091a69125d5dfcb7b8c2659029395bde", FSCIdentity: "endorser-b"},
	}
	configB.applyDefaults()
	require.NoError(t, configB.Validate())

	resolver := &crossTMSResolver{
		network: network,
		channel: channel,
		order:   []token2.TMSID{tmsA, tmsB}, // tms-a declares the network first
		configs: map[token2.TMSID]*Config{tmsA: configA, tmsB: configB},
	}

	evmClient := &mock.EVMClient{}
	evmClient.ChainIDReturns(big.NewInt(testChainID), nil)

	d := &Driver{
		resolver:    resolver,
		identities:  fakeIdentityProvider{},
		viewManager: fakeViewManager{},
	}

	// --- Step 1: the real per-(network,channel) entry point --------------------------------------
	//
	// token/services/network/network.go's Provider memoizes one *Network per (network,channel) via
	// lazy.NewProviderWithKeyMapper(key, ms.newNetwork), keyed on netId{network,channel} alone (see
	// Provider.networks / netId / key() in that file) -- the TMS/namespace is not part of the key. So
	// networkProvider.newNetwork calls d.New(network, channel) exactly ONCE for this pair, no matter
	// how many TMS share it, and the resulting *Network is reused by every one of them.
	//
	// Reproduce that here and confirm it: Driver.New must resolve configuration exactly once.
	n, err := driverNewWithClient(d, network, channel, evmClient)
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, 1, resolver.configForCalls,
		"Driver.New must resolve the network's configuration exactly once: in production this call "+
			"happens once per (network,channel), memoized, regardless of how many TMS share it -- there "+
			"is no per-TMS resolution to observe here because none exists")

	// --- Step 2: the real per-TMS entry point -----------------------------------------------------
	//
	// Provider.Connect walks every configured TMS and, for each, calls GetNetwork(tmsID.Network,
	// tmsID.Channel).Connect(tmsID.Namespace) -- GetNetwork returns the SAME memoized *Network for
	// both TMS A and TMS B, so Connect is what runs per TMS, on one shared object. Network.Connect
	// (network.go:140-164) only checks reachability/chain id and starts recovery; it does not
	// rebuild, re-scope or namespace anything about the reader, submitter or endorsement factory.
	_, err = n.Connect(tmsA.Namespace)
	require.NoError(t, err)
	_, err = n.Connect(tmsB.Namespace)
	require.NoError(t, err)

	// --- Observation 1: reads --------------------------------------------------------------------
	//
	// QueryTokens takes a namespace argument (network.go:359) but never uses it to pick a
	// TokenState: it reads through n.reader, built once in NewNetwork from whichever config ConfigFor
	// resolved. Ask for TMS B's namespace and see whose contract actually gets called.
	// Connect's endorsement-policy check reads the chain too, so the read under observation is counted
	// from where that left off rather than from zero.
	beforeQuery := evmClient.CallCallCount()

	evmClient.CallReturns(abiBytes([]byte("owned-by-a")), nil)
	out, err := n.QueryTokens(t.Context(), tmsB.Namespace, []*token.ID{{TxId: anchorHex(0x01), Index: 0}})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, []byte("owned-by-a"), out[0])

	require.Equal(t, beforeQuery+1, evmClient.CallCallCount())
	_, calledTo, _, _ := evmClient.CallArgsForCall(beforeQuery)
	assert.Equal(t, addrA, calledTo,
		"QueryTokens for the tms-b namespace silently reads through TMS A's TokenState clone")
	assert.NotEqual(t, addrB, calledTo, "it must not be tms-b's own configured TokenState")

	// --- Observation 2: the submitter --------------------------------------------------------------
	//
	// There is exactly one Submitter for the whole Network, built once in Driver.New/newSubmitter
	// from the same single config. Broadcast (network.go:264) does not even take a TMS/namespace
	// argument, so there is structurally no way for it to route TMS B's transaction anywhere but
	// through that one submitter, targeting whatever TokenState it was built with.
	evmClient.PendingNonceAtReturns(3, nil)
	evmClient.EstimateGasReturns(100_000, nil)
	evmClient.SuggestGasFeesReturns(client.GasFees{
		MaxFeePerGas:         big.NewInt(20_000_000_000),
		MaxPriorityFeePerGas: big.NewInt(1_000_000_000),
	}, nil)
	evmClient.SendRawTransactionReturns(client.Hash{}, nil)

	env := &Envelope{
		Anchor:       anchorHex(0xA1),
		Delta:        testDelta(),
		Endorsements: [][]byte{make([]byte, 65)},
	}
	require.NoError(t, n.Broadcast(t.Context(), env)) // this stands in for TMS B's own broadcast

	require.Equal(t, 1, evmClient.EstimateGasCallCount())
	_, estimateMsg := evmClient.EstimateGasArgsForCall(0)
	require.NotNil(t, estimateMsg.To)
	assert.Equal(t, addrA, *estimateMsg.To,
		"the shared submitter targets TMS A's TokenState for a transaction that has nothing to do with TMS A")
	assert.NotEqual(t, addrB, *estimateMsg.To)

	// --- Observation 3: the EIP-712 domain ----------------------------------------------------------
	//
	// installEndorsement (driver.go:266-320) builds ONE endorsement.ServiceFactory carrying ONE
	// eip712.Domain{VerifyingContract: <config.TokenStateAddress()>} (driver.go:277-285), from the
	// exact same config object NewNetwork used. ServiceFactory.ForTMS (endorsement/esp.go:104-141)
	// only varies the resolved RequestValidator per TMS id; domain, tokenState, client, registry and
	// threshold are all single fields on the factory, reused unchanged for every TMS it ever builds a
	// Service for. Get the actual *endorsement.Service the driver built for TMS B and read its live
	// domain rather than re-deriving what it "should" be.
	svcB, err := n.endorsementForID(tmsB)
	require.NoError(t, err)
	domainB := extractDomain(t, svcB)
	assert.Equal(t, addrA, domainB.VerifyingContract,
		"TMS B's endorsement service signs and verifies EIP-712 digests against TMS A's TokenState address")
	assert.NotEqual(t, addrB, domainB.VerifyingContract)
	assert.Equal(t, configA.ChainIDBig().String(), domainB.ChainID.String())
}

// repeat returns s repeated n times, a tiny local helper so the two test addresses above are
// visibly-constructed, easy-to-eyeball hex strings instead of opaque literals.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}

	return string(out)
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

// Round 4, target #4: multi-TMS cross-contamination, built and observed live. Originally the
// regression test for deferred finding #3 (BUG_HUNT_PROMPT.md's "known, deliberately deferred"
// list): configNetworkResolver.ConfigFor picked one *Config for the first TMS to declare an EVM
// network/channel, and token/services/network/network.go's Provider memoizes one *Network per
// (network, channel) with the namespace NOT part of the memoization key. Every other TMS sharing
// that network/channel was therefore routed, silently, through the first TMS's Contracts.TokenState,
// EndorsementVerifier/EIP-712 domain, Submitter and Gas policy.
//
// Fixed by resolving configuration per TMS (ConfigForTMS) instead of once for the whole network, and
// giving *Network a namespaceBinding per TMS instead of one shared reader/submitter/finality manager.
// KEPT as the isolation regression test the original comment promised: it now asserts the opposite of
// what it used to - that TMS B's reads, writes and endorsement domain go through TMS B's own
// TokenState, never TMS A's, even though both share one *Network object under the hood.

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

// crossTMSResolver simulates configNetworkResolver's real behavior for two TMS sharing one
// network/channel, each carrying its own EVM configuration with a distinct TokenState.
//
// configForCalls and configForTMSCalls count how many times each method was asked, so the test can
// pin the exact resolution shape the fix relies on: the network-wide client is bootstrapped from
// ConfigFor exactly once, while every TMS gets its own ConfigForTMS resolution.
type crossTMSResolver struct {
	network, channel string
	// order lists the TMS ids in the order configNetworkResolver.TMSIDsFor would return them; order[0]
	// is "the first TMS to declare it", which is what ConfigFor still keys off for the network-wide
	// bootstrap values (endpoint, chain id).
	order             []token2.TMSID
	configs           map[token2.TMSID]*Config
	configForCalls    int
	configForTMSCalls int
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

func (r *crossTMSResolver) ConfigForTMS(tmsID token2.TMSID) (*Config, error) {
	r.configForTMSCalls++
	cfg, ok := r.configs[tmsID]
	if !ok {
		return nil, errors.Errorf("no evm configuration for [%s]", tmsID)
	}

	return cfg, nil
}

func (r *crossTMSResolver) ConfigurationFor(tmsID token2.TMSID) (*config.Configuration, error) {
	return nil, errors.Errorf("no configuration for [%s]", tmsID)
}

// driverNewWithClient reproduces Driver.New's body line for line, with exactly one substitution: the
// real client.NewJSONRPCClient(config.Endpoint, nil) call is replaced with an already-constructed
// client.EVMClient (a *mock.EVMClient in this test).
//
// New has no seam to inject a client -- it always dials the endpoint for real -- and this test needs
// to observe every chain-facing call the constructed Network, Submitter and endorsement factory make,
// which a live TCP dial cannot give it. Every other line -- resolving every TMS's own configuration
// through the resolver, newSubmitter, NewNetwork, installEndorsement, watchPublicParams, the
// recovery-starter wiring -- is copied unchanged from New, and it is exactly that part the test
// exercises; only the transport, which the cross-TMS routing bug has nothing to do with, differs.
func driverNewWithClient(d *Driver, network, channel string, evmClient client.EVMClient) (*Network, error) {
	if !d.resolver.IsEVMNetwork(network, channel) {
		return nil, errors.Errorf("evm: no evm network configuration for [%s:%s]", network, channel)
	}
	tmsIDs := d.resolver.TMSIDsFor(network, channel)
	if len(tmsIDs) == 0 {
		return nil, errors.Errorf("evm: no tms declares network [%s:%s]", network, channel)
	}

	networkConfig, err := d.resolver.ConfigFor(network, channel)
	if err != nil {
		return nil, err
	}

	namespaces := make([]NamespaceConfig, 0, len(tmsIDs))
	for _, tmsID := range tmsIDs {
		tmsConfig, err := d.resolver.ConfigForTMS(tmsID)
		if err != nil {
			return nil, err
		}
		if tmsConfig.Endpoint != networkConfig.Endpoint || tmsConfig.ChainID != networkConfig.ChainID {
			return nil, errors.Errorf("tms [%s] disagrees with [%s:%s] on endpoint/chain id", tmsID, network, channel)
		}
		submitter, err := d.newSubmitter(tmsConfig, evmClient)
		if err != nil {
			return nil, err
		}
		namespaces = append(namespaces, NamespaceConfig{Namespace: tmsID.Namespace, Config: tmsConfig, Submitter: submitter})
	}

	n, err := NewNetwork(network, evmClient, namespaces, nil, d.membership)
	if err != nil {
		return nil, err
	}
	if err := d.installEndorsement(n, namespaces, evmClient, network, channel); err != nil {
		return nil, err
	}
	d.watchPublicParams(network, channel, namespaces, evmClient)
	n.SetRecoveryStarter(func(ns string) error {
		return d.startRecovery(token2.TMSID{Network: network, Channel: channel, Namespace: ns}, n)
	})

	return n, nil
}

// extractDomain reads the live, unexported eip712.Domain field off a *endorsement.Service returned
// through the EndorsementService interface, via reflection. This is test-only introspection -- no
// production code is touched or needs to be -- used because endorsement.Service.domain has no
// exported accessor and the point of this test is to observe the actual object the driver built for
// each TMS, not to re-derive what it "should" contain.
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

// TestMultiTMSIsolation_Live builds two TMS on one EVM network/channel, each with its own, different
// TokenState clone, drives them through the actual production entry points (Driver.New once,
// Network.Connect per TMS -- confirmed below by call-counting rather than assumed), and observes --
// live, on the constructed objects, not by re-reading source -- that TMS B's reads, its submitter and
// its EIP-712 signing domain all go through TMS B's own TokenState, never TMS A's, even though a
// single *Network object serves both.
func TestMultiTMSIsolation_Live(t *testing.T) {
	const network = "evm-net"
	const channel = ""

	tmsA := token2.TMSID{Network: network, Channel: channel, Namespace: "tms-a"}
	tmsB := token2.TMSID{Network: network, Channel: channel, Namespace: "tms-b"}

	// Two TMS, two genuinely different TokenState clones.
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
	configB.Submitter = SubmitterConfig{Keystore: writeKey(t, testKeyHex), Address: testKeyAddress}
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
	// Both TokenState addresses in this test are placeholders that no chain has ever seen. What is
	// under observation here is which of them each TMS's own binding routes through, not whether
	// either is deployed, so the node reports code at every address and Connect's deployment check
	// passes.
	evmClient.CodeAtReturns([]byte{0x60, 0x00}, nil)

	d := &Driver{
		resolver:    resolver,
		identities:  fakeIdentityProvider{},
		viewManager: fakeViewManager{},
	}

	// --- Step 1: the real per-(network,channel) entry point --------------------------------------
	//
	// token/services/network/network.go's Provider memoizes one *Network per (network,channel) via
	// lazy.NewProviderWithKeyMapper(key, ms.newNetwork), keyed on netId{network,channel} alone -- the
	// TMS/namespace is not part of the key. So networkProvider.newNetwork calls d.New(network,
	// channel) exactly ONCE for this pair, no matter how many TMS share it, and the resulting
	// *Network is reused by every one of them. Reproduce that here and confirm it: ConfigFor (the
	// network-wide bootstrap) resolves exactly once, while ConfigForTMS resolves once per TMS -- the
	// per-TMS resolution this fix adds.
	n, err := driverNewWithClient(d, network, channel, evmClient)
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, 1, resolver.configForCalls,
		"Driver.New must resolve the network-wide bootstrap configuration exactly once")
	assert.Equal(t, 2, resolver.configForTMSCalls,
		"Driver.New must resolve each of the two TMS's own configuration once")

	// --- Step 2: the real per-TMS entry point -----------------------------------------------------
	//
	// Provider.Connect walks every configured TMS and, for each, calls GetNetwork(tmsID.Network,
	// tmsID.Channel).Connect(tmsID.Namespace) -- GetNetwork returns the SAME memoized *Network for
	// both TMS A and TMS B, so Connect is what runs per TMS, on one shared object.
	_, err = n.Connect(tmsA.Namespace)
	require.NoError(t, err)
	_, err = n.Connect(tmsB.Namespace)
	require.NoError(t, err)

	// --- Observation 1: reads --------------------------------------------------------------------
	//
	// QueryTokens takes a namespace argument and now uses it to pick the right namespaceBinding.
	// Connect's endorsement-policy check reads the chain too, so the read under observation is
	// counted from where that left off rather than from zero.
	beforeQuery := evmClient.CallCallCount()

	evmClient.CallReturns(abiBytes([]byte("owned-by-b")), nil)
	out, err := n.QueryTokens(t.Context(), tmsB.Namespace, []*token.ID{{TxId: anchorHex(0x01), Index: 0}})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, []byte("owned-by-b"), out[0])

	require.Equal(t, beforeQuery+1, evmClient.CallCallCount())
	_, calledTo, _, _ := evmClient.CallArgsForCall(beforeQuery)
	assert.Equal(t, addrB, calledTo, "QueryTokens for the tms-b namespace must read through tms-b's own TokenState clone")
	assert.NotEqual(t, addrA, calledTo, "it must not silently fall back to tms-a's TokenState")

	// The same holds in the other direction: tms-a's own reads must still go through tms-a's contract,
	// unaffected by tms-b sharing the same *Network object.
	beforeQueryA := evmClient.CallCallCount()
	evmClient.CallReturns(abiBytes([]byte("owned-by-a")), nil)
	outA, err := n.QueryTokens(t.Context(), tmsA.Namespace, []*token.ID{{TxId: anchorHex(0x02), Index: 0}})
	require.NoError(t, err)
	require.Len(t, outA, 1)
	_, calledToA, _, _ := evmClient.CallArgsForCall(beforeQueryA)
	assert.Equal(t, addrA, calledToA, "tms-a's own reads must still go through tms-a's own TokenState clone")

	// --- Observation 2: the submitter --------------------------------------------------------------
	//
	// Each namespace now has its own Submitter, resolved from its own config. Broadcast has no
	// namespace argument of its own, so the envelope carries it: RequestApproval/SetupPublicParams
	// fill Envelope.Namespace in from the TMS they were called for, and Broadcast routes on it.
	evmClient.PendingNonceAtReturns(3, nil)
	evmClient.EstimateGasReturns(100_000, nil)
	evmClient.SuggestGasFeesReturns(client.GasFees{
		MaxFeePerGas:         big.NewInt(20_000_000_000),
		MaxPriorityFeePerGas: big.NewInt(1_000_000_000),
	}, nil)
	evmClient.SendRawTransactionReturns(client.Hash{}, nil)

	env := &Envelope{
		Anchor:       anchorHex(0xA1),
		Namespace:    tmsB.Namespace,
		Delta:        testDelta(),
		Endorsements: [][]byte{make([]byte, 65)},
	}
	require.NoError(t, n.Broadcast(t.Context(), env))

	require.Equal(t, 1, evmClient.EstimateGasCallCount())
	_, estimateMsg := evmClient.EstimateGasArgsForCall(0)
	require.NotNil(t, estimateMsg.To)
	assert.Equal(t, addrB, *estimateMsg.To, "tms-b's own submitter must target tms-b's own TokenState")
	assert.NotEqual(t, addrA, *estimateMsg.To)

	// --- Observation 3: the EIP-712 domain ----------------------------------------------------------
	//
	// installEndorsement now Registers each TMS's own Domain/TokenState/PublicParams/Registry/
	// Threshold with the shared endorsement.ServiceFactory, and ForTMS resolves them by TMS id instead
	// of reusing one factory-wide set. Get the actual *endorsement.Service the driver built for TMS B
	// and read its live domain rather than re-deriving what it "should" be.
	svcB, err := n.endorsementForID(tmsB)
	require.NoError(t, err)
	domainB := extractDomain(t, svcB)
	assert.Equal(t, addrB, domainB.VerifyingContract, "TMS B's endorsement service must sign and verify against TMS B's own TokenState")
	assert.NotEqual(t, addrA, domainB.VerifyingContract)
	assert.Equal(t, configB.ChainIDBig().String(), domainB.ChainID.String())

	// And TMS A's own domain must be unaffected.
	svcA, err := n.endorsementForID(tmsA)
	require.NoError(t, err)
	domainA := extractDomain(t, svcA)
	assert.Equal(t, addrA, domainA.VerifyingContract, "TMS A's endorsement service must still sign and verify against TMS A's own TokenState")
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

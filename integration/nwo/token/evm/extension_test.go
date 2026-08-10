/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"

	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	evmdriver "github.com/LFDT-Panurus/panurus/x/token/services/network/evm"
	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	evmnwo "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/nwo"
)

const (
	tokenStateAddr = "0x5fbdb2315678afecb367f032d93f642f64180aa3" // #nosec G101 -- contract address, not a credential
	verifierAddr   = "0xe7f1725e7734ce288f8367e1bb143e90bb3f0512"
	endorserAddr   = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
	submitterAddr  = "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf"
)

func testTMS() *topology2.TMS {
	return &topology2.TMS{
		Network:   "evm",
		Channel:   "",
		Namespace: "token",
		Driver:    "zkatdlognoghv1",
	}
}

// testWallets is one node's wallets, the way the materials handler issues them: the identities that
// node actually holds, not the union over the network.
func testWallets() *topology2.Wallets {
	return &topology2.Wallets{
		Owners: []topology2.Identity{{ID: "alice", Path: "/crypto/alice/owner", Default: true}},
	}
}

func testNodeConfig() NodeConfig {
	tokenState, _ := evmclient.HexToAddress(tokenStateAddr)
	verifier, _ := evmclient.HexToAddress(verifierAddr)

	return NodeConfig{
		NodeName:   "alice",
		Endpoint:   "http://127.0.0.1:8545",
		ChainID:    31337,
		Deployment: evmnwo.Deployment{TokenState: tokenState, EndorsementVerifier: verifier},
		IsEndorser: true, EndorserKeystore: "/keys/alice.key", EndorserAddress: endorserAddr,
		SubmitterKeystore: "/keys/submitter.key", SubmitterAddress: submitterAddr,
		Threshold: 1,
		Allowlist: []string{"alice", "bob"},
		Endorsers: []EndorserBinding{{Address: endorserAddr, FSCIdentity: "alice"}},
	}
}

// yamlConfiguration reads the rendered document the way the SDK's configuration service does, so the
// driver's own loader can be pointed at it.
type yamlConfiguration struct {
	doc map[any]any
}

func (c *yamlConfiguration) lookup(key string) (any, bool) {
	var cur any = c.doc
	for part := range strings.SplitSeq(key, ".") {
		m, ok := cur.(map[any]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}

	return cur, true
}

func (c *yamlConfiguration) IsSet(key string) bool {
	_, ok := c.lookup(key)

	return ok
}

func (c *yamlConfiguration) UnmarshalKey(key string, rawVal any) error {
	v, ok := c.lookup(key)
	if !ok {
		return nil
	}
	raw, err := yaml.Marshal(v)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(raw, rawVal)
}

// TestExtensionRoundTrips is the contract between the test network and the driver: whatever this
// template emits must be exactly what the driver's own loader accepts. Asserting against a
// hand-written expectation would only prove the template matches itself, so the rendered document is
// parsed by LoadConfig and the resulting struct is checked.
func TestExtensionRoundTrips(t *testing.T) {
	rendered, err := RenderExtension(testTMS(), testWallets(), testNodeConfig())
	require.NoError(t, err)

	var doc map[any]any
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &doc), "the extension must be valid yaml:\n%s", rendered)

	// Descend to the TMS block, the way the configuration service scopes a TMS.
	cfg := &yamlConfiguration{doc: doc}
	tmsBlock, ok := cfg.lookup("token.tms." + testTMS().TmsID())
	require.True(t, ok, "the tms block must be present:\n%s", rendered)
	scoped := &yamlConfiguration{doc: tmsBlock.(map[any]any)}

	require.True(t, scoped.IsSet(evmdriver.EVMConfigKey),
		"the driver detects an evm network by this key, so it must be set:\n%s", rendered)

	loaded, err := evmdriver.LoadConfig(scoped)
	require.NoError(t, err, "the driver must accept its own generated configuration:\n%s", rendered)

	assert.Equal(t, "http://127.0.0.1:8545", loaded.Endpoint)
	assert.Equal(t, int64(31337), loaded.ChainID)
	assert.Equal(t, tokenStateAddr, strings.ToLower(loaded.Contracts.TokenState))
	assert.Equal(t, uint(1), loaded.Endorsement.Threshold)
	require.Len(t, loaded.Endorsement.Endorsers, 1)
	assert.Equal(t, endorserAddr, strings.ToLower(loaded.Endorsement.Endorsers[0].Address))
	assert.Equal(t, "alice", loaded.Endorsement.Endorsers[0].FSCIdentity)
	assert.Equal(t, []string{"alice", "bob"}, loaded.Endorsement.Allowlist)
	assert.True(t, loaded.Endorser.Enabled)
	assert.Equal(t, "/keys/submitter.key", loaded.Submitter.Keystore)
}

// TestExtensionForNonEndorser checks the shape of a node that neither endorses nor broadcasts: it
// still needs the endorser set to route requests, but carries no keys of its own.
func TestExtensionForNonEndorser(t *testing.T) {
	cfg := testNodeConfig()
	cfg.NodeName = "charlie"
	cfg.IsEndorser = false
	cfg.EndorserKeystore, cfg.EndorserAddress = "", ""
	cfg.SubmitterKeystore, cfg.SubmitterAddress = "", ""

	rendered, err := RenderExtension(testTMS(), testWallets(), cfg)
	require.NoError(t, err)

	var doc map[any]any
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &doc))
	tmsBlock, ok := (&yamlConfiguration{doc: doc}).lookup("token.tms." + testTMS().TmsID())
	require.True(t, ok)

	loaded, err := evmdriver.LoadConfig(&yamlConfiguration{doc: tmsBlock.(map[any]any)})
	require.NoError(t, err, "a node with no keys must still produce a valid configuration:\n%s", rendered)

	assert.False(t, loaded.Endorser.Enabled)
	assert.Empty(t, loaded.Submitter.Keystore)
	assert.Len(t, loaded.Endorsement.Endorsers, 1, "it still needs the endorser set to route requests")
}

// TestExtensionDefaults checks the finality tuning defaults land in the document rather than leaving
// the driver to guess.
func TestExtensionDefaults(t *testing.T) {
	cfg := testNodeConfig()
	cfg.BlockTag, cfg.PollInterval, cfg.FinalityTimeout = "", 0, 0

	rendered, err := RenderExtension(testTMS(), testWallets(), cfg)
	require.NoError(t, err)

	var doc map[any]any
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &doc))
	tmsBlock, _ := (&yamlConfiguration{doc: doc}).lookup("token.tms." + testTMS().TmsID())
	loaded, err := evmdriver.LoadConfig(&yamlConfiguration{doc: tmsBlock.(map[any]any)})
	require.NoError(t, err)

	assert.Equal(t, DefaultBlockTag, loaded.Finality.BlockTag)
	assert.Equal(t, DefaultPollInterval, loaded.Finality.PollInterval)
	assert.Equal(t, DefaultFinalityTimeout, loaded.Finality.Timeout)
}

// TestExtensionRendersTheNodeOwnWallets is the regression test for a node that started with no usable
// identity: the wallets rendered must be the ones issued to that node. A node whose owners list is
// empty has no identity to transact with, and the failure surfaces far away, as a wallet that does
// not exist.
func TestExtensionRendersTheNodeOwnWallets(t *testing.T) {
	rendered, err := RenderExtension(testTMS(), testWallets(), testNodeConfig())
	require.NoError(t, err)

	var doc map[any]any
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &doc))
	owners, ok := (&yamlConfiguration{doc: doc}).lookup("token.tms." + testTMS().TmsID() + ".wallets.owners")
	require.True(t, ok, "the node must be given its owner wallets:\n%s", rendered)

	list, ok := owners.([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	assert.Equal(t, "alice", list[0].(map[any]any)["id"])
	assert.Equal(t, "/crypto/alice/owner", list[0].(map[any]any)["path"])
}

// TestExtensionWithoutWallets checks a node holding no wallets at all still renders a loadable
// configuration: the endorsers hold no token identities and must still start.
func TestExtensionWithoutWallets(t *testing.T) {
	rendered, err := RenderExtension(testTMS(), nil, testNodeConfig())
	require.NoError(t, err)

	var doc map[any]any
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &doc))
	tmsBlock, ok := (&yamlConfiguration{doc: doc}).lookup("token.tms." + testTMS().TmsID())
	require.True(t, ok)
	_, err = evmdriver.LoadConfig(&yamlConfiguration{doc: tmsBlock.(map[any]any)})
	require.NoError(t, err, "a node with no wallets must still produce a valid configuration:\n%s", rendered)
}

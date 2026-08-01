/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// yamlConfiguration is a Configuration backed by a real YAML document, so the tests exercise the
// actual decoding path (tags, nested blocks, durations) rather than a hand-built struct.
type yamlConfiguration struct {
	root map[string]any
}

func newYAMLConfiguration(t *testing.T, doc string) *yamlConfiguration {
	t.Helper()
	var root map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(doc), &root))

	return &yamlConfiguration{root: root}
}

// lookup walks a dotted key path.
func (c *yamlConfiguration) lookup(key string) (any, bool) {
	current := any(c.root)
	for _, part := range splitKey(key) {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}

	return current, true
}

func (c *yamlConfiguration) IsSet(key string) bool {
	_, ok := c.lookup(key)

	return ok
}

func (c *yamlConfiguration) UnmarshalKey(key string, rawVal any) error {
	v, ok := c.lookup(key)
	if !ok {
		return nil // absent key leaves the target zero, matching the SDK's behaviour
	}
	raw, err := yaml.Marshal(v)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(raw, rawVal)
}

func splitKey(key string) []string {
	var out []string
	start := 0
	for i := range len(key) {
		if key[i] == '.' {
			out = append(out, key[start:i])
			start = i + 1
		}
	}

	return append(out, key[start:])
}

const fullConfigYAML = `
services:
  network:
    evm:
      endpoint: http://localhost:8545
      chainID: 31337
      contracts:
        tokenState: "0x5FbDB2315678afecb367f032d93F642f64180aa3"
        endorsementVerifier: "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512"
      finality:
        blockTag: finalized
        pollInterval: 2s
        timeout: 5m
      gas:
        strategy: estimate
        multiplier: 1.5
      endorser:
        enabled: true
        keystore: /keys/endorser
        address: "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
        fscIdentity: alice
      submitter:
        keystore: /keys/submitter
        address: "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf"
      endorsement:
        threshold: 2
        allowlist: [alice, bob]
        endorsers:
          - address: "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
            fscIdentity: alice
          - address: "0x2b5ad5c4795c026514f8317c7a215e218dccd6cf"
            fscIdentity: bob
`

func TestLoadConfigFullDocument(t *testing.T) {
	c, err := LoadConfig(newYAMLConfiguration(t, fullConfigYAML))
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:8545", c.Endpoint)
	assert.Equal(t, int64(31337), c.ChainID)
	assert.Equal(t, int64(31337), c.ChainIDBig().Int64())
	assert.Equal(t, "finalized", c.Finality.BlockTag)
	assert.Equal(t, 2*time.Second, c.Finality.PollInterval)
	assert.Equal(t, 5*time.Minute, c.Finality.Timeout)
	assert.InEpsilon(t, 1.5, c.Gas.Multiplier, 1e-9)
	assert.True(t, c.Endorser.Enabled)
	assert.Equal(t, uint(2), c.Endorsement.Threshold)
	require.Len(t, c.Endorsement.Endorsers, 2)
	assert.Equal(t, "alice", c.Endorsement.Endorsers[0].FSCIdentity)
	assert.Equal(t, []string{"alice", "bob"}, c.Endorsement.Allowlist)

	addr, err := c.TokenStateAddress()
	require.NoError(t, err)
	assert.Equal(t, "0x5fbdb2315678afecb367f032d93f642f64180aa3", addr.Hex())
}

// minimalConfigYAML omits every optional block, so the defaults must make it usable.
const minimalConfigYAML = `
services:
  network:
    evm:
      endpoint: http://localhost:8545
      chainID: 1337
      contracts:
        tokenState: "0x5FbDB2315678afecb367f032d93F642f64180aa3"
      endorsement:
        threshold: 1
        endorsers:
          - address: "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
            fscIdentity: alice
`

func TestLoadConfigAppliesDefaults(t *testing.T) {
	c, err := LoadConfig(newYAMLConfiguration(t, minimalConfigYAML))
	require.NoError(t, err)

	assert.Equal(t, DefaultBlockTag, c.Finality.BlockTag)
	assert.Equal(t, DefaultPollInterval, c.Finality.PollInterval)
	assert.Equal(t, DefaultFinalityTimeout, c.Finality.Timeout)
	assert.Equal(t, DefaultGasStrategy, c.Gas.Strategy)
	assert.InEpsilon(t, DefaultGasMultiplier, c.Gas.Multiplier, 1e-9)
}

// TestConfigKeyIsSetOnRealYAML settles the Week-1 review question: whether IsSet on the parent key
// (services.network.evm) actually distinguishes an EVM TMS from a non-EVM one, since that is what
// Driver.New routes on. It is checked here against real YAML, with both a leaf and the parent key.
func TestConfigKeyIsSetOnRealYAML(t *testing.T) {
	evmTMS := newYAMLConfiguration(t, fullConfigYAML)
	assert.True(t, evmTMS.IsSet(EVMConfigKey), "an evm TMS must be detected by the parent key")
	assert.True(t, evmTMS.IsSet(EVMConfigKey+".endpoint"), "and by a leaf key")

	fabricTMS := newYAMLConfiguration(t, `
services:
  network:
    fabric:
      channel: testchannel
`)
	assert.False(t, fabricTMS.IsSet(EVMConfigKey), "a non-evm TMS must not be detected")
	assert.False(t, fabricTMS.IsSet(EVMConfigKey+".endpoint"))
}

func TestConfigValidationRejectsBadDocuments(t *testing.T) {
	base := func() *Config {
		c, err := LoadConfig(newYAMLConfiguration(t, fullConfigYAML))
		require.NoError(t, err)

		return c
	}

	cases := map[string]func(*Config){
		"empty endpoint":          func(c *Config) { c.Endpoint = "" },
		"zero chain id":           func(c *Config) { c.ChainID = 0 },
		"negative chain id":       func(c *Config) { c.ChainID = -1 },
		"missing token state":     func(c *Config) { c.Contracts.TokenState = "" },
		"malformed token state":   func(c *Config) { c.Contracts.TokenState = "0xdeadbeef" },
		"malformed verifier":      func(c *Config) { c.Contracts.EndorsementVerifier = "not-an-address" },
		"unsupported block tag":   func(c *Config) { c.Finality.BlockTag = "pending" },
		"unknown gas strategy":    func(c *Config) { c.Gas.Strategy = "guess" },
		"multiplier below one":    func(c *Config) { c.Gas.Multiplier = 0.5 },
		"fixed gas without limit": func(c *Config) { c.Gas.Strategy = GasStrategyFixed; c.Gas.Limit = 0 },
		"no endorsers":            func(c *Config) { c.Endorsement.Endorsers = nil },
		"zero threshold":          func(c *Config) { c.Endorsement.Threshold = 0 },
		"threshold above set": func(c *Config) {
			c.Endorsement.Threshold = uint(len(c.Endorsement.Endorsers)) + 1
		},
		"endorser without identity": func(c *Config) { c.Endorsement.Endorsers[0].FSCIdentity = "" },
		"endorser with bad address": func(c *Config) { c.Endorsement.Endorsers[0].Address = "0x1234" },
		"duplicate endorser address": func(c *Config) {
			c.Endorsement.Endorsers[1].Address = c.Endorsement.Endorsers[0].Address
		},
		"enabled endorser without address": func(c *Config) { c.Endorser.Address = "" },
		"enabled endorser bad address":     func(c *Config) { c.Endorser.Address = "nope" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(c)
			require.Error(t, c.Validate())
		})
	}
}

// TestFixedGasStrategyIsValid checks the fixed strategy is accepted when a limit is set.
func TestFixedGasStrategyIsValid(t *testing.T) {
	c, err := LoadConfig(newYAMLConfiguration(t, fullConfigYAML))
	require.NoError(t, err)
	c.Gas.Strategy = GasStrategyFixed
	c.Gas.Limit = 500_000
	require.NoError(t, c.Validate())
}

// TestThresholdEqualToSetSizeIsValid checks the boundary: an N-of-N policy is legitimate.
func TestThresholdEqualToSetSizeIsValid(t *testing.T) {
	c, err := LoadConfig(newYAMLConfiguration(t, fullConfigYAML))
	require.NoError(t, err)
	c.Endorsement.Threshold = uint(len(c.Endorsement.Endorsers))
	require.NoError(t, c.Validate())
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	topology2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"
	identityconfig "github.com/LFDT-Panurus/panurus/token/services/identity/config"
	sfcnode "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// renderedTMS renders the TMS extension for a node and returns its TMS section.
func renderedTMS(t *testing.T, tms *topology2.TMS, wallets *topology2.Wallets, opts ...sfcnode.Option) map[string]any {
	t.Helper()
	gomega.RegisterTestingT(t)
	handler := &fabric.NetworkHandler{Entries: map[string]*fabric.Entry{}}
	if wallets != nil {
		handler.Entries["e"] = &fabric.Entry{TMS: tms, Wallets: map[string]*topology2.Wallets{"alice": wallets}}
	}
	extension := handler.GenerateExtension(tms, sfcnode.NewNode("alice").AddOptions(opts...), "alice")

	var parsed struct {
		Token struct {
			TMS map[string]map[string]any `yaml:"tms"`
		} `yaml:"token"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(extension), &parsed), "extension is not valid YAML:\n%s", extension)
	section, ok := parsed.Token.TMS[tms.TmsID()]
	require.True(t, ok, "missing TMS section:\n%s", extension)

	return section
}

func testTMS() *topology2.TMS {
	return &topology2.TMS{Network: "testnet", Channel: "testchannel", Namespace: "tns", Driver: "fabtoken", BackendParams: map[string]any{}}
}

// walletsConfig decodes the wallets section as the identity configuration does.
func walletsConfig(t *testing.T, section map[string]any) identityconfig.Wallets {
	t.Helper()
	raw, err := yaml.Marshal(section["wallets"])
	require.NoError(t, err)
	var w identityconfig.Wallets
	require.NoError(t, yaml.Unmarshal(raw, &w))

	return w
}

func cleanupEnabled(t *testing.T, section map[string]any) any {
	t.Helper()
	services, ok := section["services"].(map[string]any)
	require.True(t, ok)
	storage, ok := services["storage"].(map[string]any)
	require.True(t, ok)
	cleanup, ok := storage["cleanup"].(map[string]any)
	require.True(t, ok)

	return cleanup["enabled"]
}

func TestGenerateExtensionDefaults(t *testing.T) {
	for name, wallets := range map[string]*topology2.Wallets{
		"no topology wallets": nil,
		"owner wallet":        {Owners: []topology2.Identity{{ID: "alice", Default: true, Path: "/tmp/alice"}}},
	} {
		t.Run(name, func(t *testing.T) {
			section := renderedTMS(t, testTMS(), wallets)
			w := walletsConfig(t, section)
			assert.Equal(t, topology2.DefaultOwnerCacheSize, w.DefaultCacheSize)
			if wallets != nil {
				require.Len(t, w.Owners, 1)
				assert.Equal(t, "alice", w.Owners[0].ID)
			}
			assert.Equal(t, true, cleanupEnabled(t, section))
		})
	}
}

func TestGenerateExtensionOptions(t *testing.T) {
	section := renderedTMS(t, testTMS(), nil, token.WithDefaultCacheSize(10), token.WithStorageCleanupDisabled())
	assert.Equal(t, 10, walletsConfig(t, section).DefaultCacheSize)
	assert.Equal(t, false, cleanupEnabled(t, section))
}

func TestWithDefaultCacheSizeRejectsNegative(t *testing.T) {
	require.Error(t, token.WithDefaultCacheSize(-1)(&sfcnode.Options{Mapping: map[string]any{}}))
}

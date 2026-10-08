/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	tokenconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadSelectionFromYAML writes endorsementYAML into a TMS stanza of a real core.yaml,
// loads it through the production configuration stack, and returns the resulting
// selection. It is what proves the documented YAML keys actually reach
// LoadEndorserSelection: the unit tests above fake the configuration accessors, so they
// cannot catch a key whose spelling or nesting is wrong.
func loadSelectionFromYAML(t *testing.T, endorsementYAML string) (config.EndorserSelection, error) {
	t.Helper()

	dir := t.TempDir()
	raw := `
fsc:
  id: test-node
token:
  enabled: true
  tms:
    mytms:
      network: testnet
      channel: testchannel
      namespace: testns
      services:
        network:
          fabric:
` + endorsementYAML
	require.NoError(t, os.WriteFile(filepath.Join(dir, "core.yaml"), []byte(raw), 0o600))

	provider, err := fscconfig.NewProvider(dir)
	require.NoError(t, err)
	configuration, err := tokenconfig.NewService(provider).ConfigurationFor("testnet", "testchannel", "testns")
	require.NoError(t, err)

	return config.LoadEndorserSelection(configuration)
}

func TestLoadEndorserSelectionFromRealYAML(t *testing.T) {
	t.Run("no endorsement stanza keeps default discovery", func(t *testing.T) {
		selection, err := loadSelectionFromYAML(t, "            recovery:\n              enabled: true\n")
		require.NoError(t, err)
		assert.False(t, selection.IsSet(), "an unconfigured node must keep Fabric's default discovery")
	})

	t.Run("an empty endorsement stanza selects nothing", func(t *testing.T) {
		selection, err := loadSelectionFromYAML(t, "            endorsement:\n")
		require.NoError(t, err)
		assert.False(t, selection.IsSet())
	})

	t.Run("an empty mspIDs list selects nothing", func(t *testing.T) {
		selection, err := loadSelectionFromYAML(t, "            endorsement:\n              mspIDs: []\n")
		require.NoError(t, err)
		assert.False(t, selection.IsSet())
	})

	t.Run("mspIDs", func(t *testing.T) {
		selection, err := loadSelectionFromYAML(t, "            endorsement:\n              mspIDs:\n              - Org1MSP\n              - Org3MSP\n")
		require.NoError(t, err)
		assert.Equal(t, config.EndorserSelection{MSPIDs: []string{"Org1MSP", "Org3MSP"}}, selection)
	})

	t.Run("a repeated MSP ID is rejected", func(t *testing.T) {
		_, err := loadSelectionFromYAML(t, "            endorsement:\n              mspIDs:\n              - Org1MSP\n              - Org1MSP\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than once")
	})

	t.Run("a blank MSP ID is rejected", func(t *testing.T) {
		_, err := loadSelectionFromYAML(t, "            endorsement:\n              mspIDs:\n              - \" \"\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty MSP ID")
	})

	t.Run("a padded MSP ID is trimmed", func(t *testing.T) {
		selection, err := loadSelectionFromYAML(t, "            endorsement:\n              mspIDs:\n              - \" Org1MSP \"\n")
		require.NoError(t, err)
		assert.Equal(t, config.EndorserSelection{MSPIDs: []string{"Org1MSP"}}, selection)
	})

	t.Run("an unrecognised endorsement key is ignored", func(t *testing.T) {
		// Only mspIDs expresses a selection. A stray key alongside it must not disturb
		// the one that counts, and on its own it leaves default discovery in charge.
		selection, err := loadSelectionFromYAML(t, "            endorsement:\n              fromMyOrg: true\n              mspIDs:\n              - Org1MSP\n")
		require.NoError(t, err)
		assert.Equal(t, config.EndorserSelection{MSPIDs: []string{"Org1MSP"}}, selection)

		selection, err = loadSelectionFromYAML(t, "            endorsement:\n              fromMyOrg: true\n")
		require.NoError(t, err)
		assert.False(t, selection.IsSet())
	})
}

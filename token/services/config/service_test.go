/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"os"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigurations tests the Configurations function
func TestConfigurations(t *testing.T) {
	// create a new config service by loading the config file
	cp, err := config.NewProvider("./testdata/token0")
	require.NoError(t, err)

	// instantiate Panurus config
	service := NewService(cp)
	checkConfigurations(t, service, 2)

	// extend
	raw, err := os.ReadFile("./testdata/token0/ext.yaml")
	require.NoError(t, err)
	require.NoError(t, service.AddConfiguration(raw))
	checkConfigurations(t, service, 3)

	configs, err := service.Configurations()
	require.NoError(t, err)
	raw, err = configs[0].Serialize(token.TMSID{
		Network:   "new_network",
		Channel:   "new_channel",
		Namespace: "new_namespace",
	})
	require.NoError(t, err)
	require.NoError(t, service.AddConfiguration(raw))
	checkConfigurations(t, service, 4)
}

func checkConfigurations(t *testing.T, service *Service, expectedTMSs int) {
	t.Helper()
	tmss, err := service.Configurations()
	require.NoError(t, err)
	assert.Len(t, tmss, expectedTMSs)
	for _, tms := range tmss {
		tmsID := tms.ID()
		tms2, err := service.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
		require.NoError(t, err)
		assert.Equal(t, tms, tms2)
	}
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks_test

import (
	"testing"
	"time"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	config2 "github.com/LFDT-Panurus/panurus/token/services/config"
	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/checks"
	config3 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	config := checks.DefaultConfig()

	assert.True(t, config.Enabled, "checks should be enabled by default")
	assert.Equal(t, time.Hour, config.ScanInterval)
	assert.Equal(t, 30*time.Minute, config.Timeout)
	assert.Equal(t, dbcommon.DefaultBatchSize, config.BatchSize)
	assert.Equal(t, time.Duration(0), config.TransactionWindow)
}

func loadConfig(t *testing.T, yaml string) *config2.Configuration {
	t.Helper()
	cp, err := (&config3.Provider{}).ProvideFromRaw([]byte(yaml))
	require.NoError(t, err)

	return config2.NewConfiguration(cp, "n1c1ns1", tdriver.TMSID{})
}

func TestLoadConfig_NoKeySet(t *testing.T) {
	cfg := loadConfig(t, `
token:
  tms:
    n1c1ns1:
      network: n1
`)

	loaded, err := checks.LoadConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, checks.DefaultConfig(), loaded)
}

func TestLoadConfig_OverridesDefaults(t *testing.T) {
	cfg := loadConfig(t, `
token:
  tms:
    n1c1ns1:
      services:
        storage:
          checks:
            enabled: true
            scanInterval: 2h
            timeout: 10m
            batchSize: 25
            transactionWindow: 5m
`)

	loaded, err := checks.LoadConfig(cfg)
	require.NoError(t, err)
	assert.True(t, loaded.Enabled)
	assert.Equal(t, 2*time.Hour, loaded.ScanInterval)
	assert.Equal(t, 10*time.Minute, loaded.Timeout)
	assert.Equal(t, 25, loaded.BatchSize)
	assert.Equal(t, 5*time.Minute, loaded.TransactionWindow)
}

func TestLoadConfig_ZeroOrNegativeFieldsFallBackToDefaults(t *testing.T) {
	cfg := loadConfig(t, `
token:
  tms:
    n1c1ns1:
      services:
        storage:
          checks:
            enabled: true
            scanInterval: -1h
            timeout: 0m
            batchSize: 0
            transactionWindow: 0s
`)

	loaded, err := checks.LoadConfig(cfg)
	require.NoError(t, err)
	defaults := checks.DefaultConfig()
	assert.Equal(t, defaults.ScanInterval, loaded.ScanInterval)
	assert.Equal(t, defaults.Timeout, loaded.Timeout)
	assert.Equal(t, defaults.BatchSize, loaded.BatchSize)
	assert.Equal(t, defaults.TransactionWindow, loaded.TransactionWindow)
}

func TestLoadConfig_EnabledStaysDefaultTrueWhenKeyPresentButUnset(t *testing.T) {
	// Setting the checks key at all, without an explicit "enabled", must not
	// disable the sweep: LoadConfig checks IsSet on the "enabled" leaf rather
	// than trusting the unmarshalled bool's zero value, so a block that only
	// tunes another field (e.g. scanInterval) keeps the documented default of
	// true.
	cfg := loadConfig(t, `
token:
  tms:
    n1c1ns1:
      services:
        storage:
          checks:
            scanInterval: 2h
`)

	loaded, err := checks.LoadConfig(cfg)
	require.NoError(t, err)
	assert.True(t, loaded.Enabled)
	assert.Equal(t, 2*time.Hour, loaded.ScanInterval)
}

func TestLoadConfig_EnabledExplicitFalseIsRespected(t *testing.T) {
	cfg := loadConfig(t, `
token:
  tms:
    n1c1ns1:
      services:
        storage:
          checks:
            enabled: false
            scanInterval: 2h
`)

	loaded, err := checks.LoadConfig(cfg)
	require.NoError(t, err)
	assert.False(t, loaded.Enabled)
	assert.Equal(t, 2*time.Hour, loaded.ScanInterval)
}

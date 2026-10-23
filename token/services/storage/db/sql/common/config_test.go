/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"testing"
	"time"

	common "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConfig is a minimal implementation of driver.Config for testing purposes.
type mockConfig struct {
	isSet     bool
	unmarshal func(key string, rawVal any) error
}

func (m *mockConfig) IsSet(_ string) bool { return m.isSet }
func (m *mockConfig) UnmarshalKey(key string, rawVal any) error {
	return m.unmarshal(key, rawVal)
}

// TestLoadTableNamesConfigAbsent checks that an empty map is returned when the
// config key is not set.
func TestLoadTableNamesConfigAbsent(t *testing.T) {
	cfg := &mockConfig{isSet: false}
	result, err := common.LoadTableNamesConfig(cfg)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestLoadTableNamesConfigPresent checks that the map is correctly populated
// when the config key is set.
func TestLoadTableNamesConfigPresent(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(_ string, rawVal any) error {
			m := rawVal.(*common.TableNamesConfig)
			*m = common.TableNamesConfig{
				"id_signers": "identity_signers",
				"tokens":     "my_tokens",
			}

			return nil
		},
	}

	result, err := common.LoadTableNamesConfig(cfg)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "identity_signers", result["id_signers"])
	assert.Equal(t, "my_tokens", result["tokens"])
}

// TestLoadStorageConfigNilConfig checks that a zero-value StorageConfig is returned for a nil config.
func TestLoadStorageConfigNilConfig(t *testing.T) {
	result, err := common.LoadStorageConfig(nil)
	require.NoError(t, err)
	assert.Empty(t, result.TableNames)
	assert.False(t, result.SkipPrefix)
}

// TestLoadStorageConfigDefaultsToNoSkip checks that SkipPrefix defaults to false
// when the key is absent from the config.
func TestLoadStorageConfigDefaultsToNoSkip(t *testing.T) {
	cfg := &mockConfig{isSet: false}
	result, err := common.LoadStorageConfig(cfg)
	require.NoError(t, err)
	assert.False(t, result.SkipPrefix)
	assert.Empty(t, result.TableNames)
}

// TestLoadStorageConfigSkipPrefixTrue checks that SkipPrefix is true when the
// config key is set to true.
func TestLoadStorageConfigSkipPrefixTrue(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			switch key {
			case common.ConfigKeySkipPrefix:
				*rawVal.(*bool) = true
			case common.ConfigKeyTableNames:
				*rawVal.(*common.TableNamesConfig) = common.TableNamesConfig{}
			}

			return nil
		},
	}
	result, err := common.LoadStorageConfig(cfg)
	require.NoError(t, err)
	assert.True(t, result.SkipPrefix)
}

// TestLoadStorageConfigTableNamesAndSkipPrefix checks that both fields are
// populated correctly when both keys are present.
func TestLoadStorageConfigTableNamesAndSkipPrefix(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			switch key {
			case common.ConfigKeySkipPrefix:
				*rawVal.(*bool) = true
			case common.ConfigKeyTableNames:
				*rawVal.(*common.TableNamesConfig) = common.TableNamesConfig{"tokens": "my_tokens"}
			}

			return nil
		},
	}
	result, err := common.LoadStorageConfig(cfg)
	require.NoError(t, err)
	assert.True(t, result.SkipPrefix)
	assert.Equal(t, "my_tokens", result.TableNames["tokens"])
}

// TestLoadStorageConfigInvalidLockStrategyPreservesOtherFields pins the #2395 Phase 7 fix: a
// typo'd lockStrategy (e.g. "skiplocked" instead of "skipLocked") must still return the
// TableNames and SkipPrefix already parsed successfully, alongside the validation error -
// not discard them by falling back to a zero-value StorageConfig. Before the fix, a caller that
// warns and continues on error (postgres/driver.go, sqlite/driver.go) would silently lose an
// operator's configured table name overrides to an unrelated typo in a different key.
func TestLoadStorageConfigInvalidLockStrategyPreservesOtherFields(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			switch key {
			case common.ConfigKeySkipPrefix:
				*rawVal.(*bool) = true
			case common.ConfigKeyTableNames:
				*rawVal.(*common.TableNamesConfig) = common.TableNamesConfig{"tokens": "my_tokens"}
			case common.ConfigKeyLockStrategy:
				*rawVal.(*string) = "skiplocked"
			}

			return nil
		},
	}

	result, err := common.LoadStorageConfig(cfg)
	require.Error(t, err, "expected an invalid lockStrategy value to be rejected")
	assert.Equal(t, "my_tokens", result.TableNames["tokens"], "TableNames must survive an unrelated lockStrategy typo")
	assert.True(t, result.SkipPrefix, "SkipPrefix must survive an unrelated lockStrategy typo")
	assert.Equal(t, common.LockStrategyInsert, result.LockStrategy, "LockStrategy must fall back to the default, not the invalid raw value")
}

// TestLoadStorageConfigPoolMetricsIntervalDefault checks that PoolMetricsInterval defaults to
// common.DefaultPoolMetricsInterval when the key is absent from the config.
func TestLoadStorageConfigPoolMetricsIntervalDefault(t *testing.T) {
	cfg := &mockConfig{isSet: false}
	result, err := common.LoadStorageConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, common.DefaultPoolMetricsInterval, result.PoolMetricsInterval)
}

// TestLoadStorageConfigPoolMetricsIntervalExplicit checks that an explicit value is parsed.
func TestLoadStorageConfigPoolMetricsIntervalExplicit(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			if key == common.ConfigKeyPoolMetricsInterval {
				*rawVal.(*string) = "5s"
			}

			return nil
		},
	}
	result, err := common.LoadStorageConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.PoolMetricsInterval)
}

// TestLoadStorageConfigPoolMetricsIntervalZeroDisables checks that 0 is accepted and preserved
// (the sampler treats it as "disabled", not as "unset").
func TestLoadStorageConfigPoolMetricsIntervalZeroDisables(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			if key == common.ConfigKeyPoolMetricsInterval {
				*rawVal.(*string) = "0"
			}

			return nil
		},
	}
	result, err := common.LoadStorageConfig(cfg)
	require.NoError(t, err)
	assert.Zero(t, result.PoolMetricsInterval)
}

// TestLoadStorageConfigPoolMetricsIntervalMalformedPreservesOtherFields checks that a malformed
// duration is rejected but, per LoadStorageConfig's documented contract, does not discard the
// other options already parsed successfully.
func TestLoadStorageConfigPoolMetricsIntervalMalformedPreservesOtherFields(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			switch key {
			case common.ConfigKeySkipPrefix:
				*rawVal.(*bool) = true
			case common.ConfigKeyPoolMetricsInterval:
				*rawVal.(*string) = "not-a-duration"
			}

			return nil
		},
	}

	result, err := common.LoadStorageConfig(cfg)
	require.Error(t, err, "expected a malformed poolMetricsInterval to be rejected")
	assert.True(t, result.SkipPrefix, "SkipPrefix must survive an unrelated poolMetricsInterval typo")
	assert.Equal(t, common.DefaultPoolMetricsInterval, result.PoolMetricsInterval, "PoolMetricsInterval must fall back to the default, not the invalid raw value")
}

// TestLoadStorageConfigPoolMetricsIntervalNegativeRejected checks that a negative duration is
// rejected rather than silently disabling the sampler the way 0 does.
func TestLoadStorageConfigPoolMetricsIntervalNegativeRejected(t *testing.T) {
	cfg := &mockConfig{
		isSet: true,
		unmarshal: func(key string, rawVal any) error {
			if key == common.ConfigKeyPoolMetricsInterval {
				*rawVal.(*string) = "-1s"
			}

			return nil
		},
	}
	result, err := common.LoadStorageConfig(cfg)
	require.Error(t, err)
	assert.Equal(t, common.DefaultPoolMetricsInterval, result.PoolMetricsInterval)
}

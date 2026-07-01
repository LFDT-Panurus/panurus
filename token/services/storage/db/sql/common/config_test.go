/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"testing"

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

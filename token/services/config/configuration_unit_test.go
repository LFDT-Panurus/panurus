/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/config/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfiguration_Validate(t *testing.T) {
	cp := &mocks.Provider{}
	tmsID := driver.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	c := config.NewConfiguration(cp, "id", tmsID)

	// Valid
	err := c.Validate()
	require.NoError(t, err)

	// Missing Network
	tmsID.Network = ""
	c = config.NewConfiguration(cp, "id", tmsID)
	err = c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required field 'network'")

	// Missing Namespace
	tmsID.Network = "net"
	tmsID.Namespace = ""
	c = config.NewConfiguration(cp, "id", tmsID)
	err = c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required field 'namespace'")

	// Custom Validator
	tmsID.Namespace = "ns"
	c = config.NewConfiguration(cp, "id", tmsID)
	validator := &mocks.Validator{}
	c.SetValidators([]config.Validator{validator})

	validator.ValidateReturns(nil)
	err = c.Validate()
	require.NoError(t, err)
	assert.Equal(t, 1, validator.ValidateCallCount())

	validator.ValidateReturns(assert.AnError)
	err = c.Validate()
	require.Error(t, err)
	assert.Equal(t, 2, validator.ValidateCallCount())
}

func TestConfiguration_Serialize(t *testing.T) {
	cp := &mocks.Provider{}
	tmsID := driver.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	c := config.NewConfiguration(cp, "old_id", tmsID)

	// Test Unmarshal Error
	cp.UnmarshalKeyReturns(assert.AnError)
	_, err := c.Serialize(token.TMSID{Network: "new"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling key")

	// Test Success
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		*rawVal.(*map[string]any) = map[string]any{"key": "value"}

		return nil
	}
	raw, err := c.Serialize(token.TMSID{Network: "new_net", Channel: "new_ch", Namespace: "new_ns"})
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
	assert.Contains(t, string(raw), "new_net")
	assert.Contains(t, string(raw), "new_ch")
	assert.Contains(t, string(raw), "new_ns")
}

func TestConfiguration_Wrappers(t *testing.T) {
	cp := &mocks.Provider{}
	tmsID := driver.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	c := config.NewConfiguration(cp, "id", tmsID)

	// ID
	assert.Equal(t, tmsID, c.ID())

	// TranslatePath
	cp.TranslatePathReturns("translated")
	assert.Equal(t, "translated", c.TranslatePath("path"))

	// UnmarshalKey
	cp.UnmarshalKeyReturns(nil)
	err := c.UnmarshalKey("key", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, cp.UnmarshalKeyCallCount())

	// GetString
	cp.GetStringReturns("string")
	assert.Equal(t, "string", c.GetString("key"))

	// GetBool
	cp.GetBoolReturns(true)
	assert.True(t, c.GetBool("key"))

	// IsSet
	cp.IsSetReturns(true)
	assert.True(t, c.IsSet("key"))
}

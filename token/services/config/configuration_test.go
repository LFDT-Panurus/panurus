/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestConfiguration(t *testing.T) *Configuration {
	t.Helper()
	cp, err := fscconfig.NewProvider("./testdata/token0")
	require.NoError(t, err)

	return NewConfiguration(cp, "n1c1ns1", driver.TMSID{
		Network:   "n1",
		Channel:   "c1",
		Namespace: "ns1",
	})
}

func TestConfiguration_ID(t *testing.T) {
	c := newTestConfiguration(t)
	id := c.ID()
	assert.Equal(t, "n1", id.Network)
	assert.Equal(t, "c1", id.Channel)
	assert.Equal(t, "ns1", id.Namespace)
}

func TestConfiguration_Validate_Valid(t *testing.T) {
	c := newTestConfiguration(t)
	assert.NoError(t, c.Validate())
}

func TestConfiguration_Validate_MissingNetwork(t *testing.T) {
	cp, err := fscconfig.NewProvider("./testdata/token0")
	require.NoError(t, err)
	c := NewConfiguration(cp, "n1c1ns1", driver.TMSID{
		Channel:   "c1",
		Namespace: "ns1",
	})
	assert.Error(t, c.Validate())
}

func TestConfiguration_Validate_MissingNamespace(t *testing.T) {
	cp, err := fscconfig.NewProvider("./testdata/token0")
	require.NoError(t, err)
	c := NewConfiguration(cp, "n1c1ns1", driver.TMSID{
		Network: "n1",
		Channel: "c1",
	})
	assert.Error(t, c.Validate())
}

func TestConfiguration_GetString(t *testing.T) {
	c := newTestConfiguration(t)
	val := c.GetString("network")
	assert.Equal(t, "n1", val)
}

func TestConfiguration_GetBool(t *testing.T) {
	cp, err := fscconfig.NewProvider("./testdata/token0")
	require.NoError(t, err)
	c := NewConfiguration(cp, "n1c1ns1", driver.TMSID{
		Network:   "n1",
		Channel:   "c1",
		Namespace: "ns1",
	})
	// non-existent bool key returns false
	assert.False(t, c.GetBool("nonexistent"))
}

func TestConfiguration_IsSet(t *testing.T) {
	c := newTestConfiguration(t)
	assert.True(t, c.IsSet("network"))
	assert.False(t, c.IsSet("nonexistent_key_xyz"))
}

func TestConfiguration_TranslatePath(t *testing.T) {
	c := newTestConfiguration(t)
	result := c.TranslatePath("some/path")
	assert.NotEmpty(t, result)
}

func TestConfiguration_Serialize(t *testing.T) {
	c := newTestConfiguration(t)
	raw, err := c.Serialize(token.TMSID{
		Network:   "new_network",
		Channel:   "new_channel",
		Namespace: "new_namespace",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
	assert.Contains(t, string(raw), "new_network")
	assert.Contains(t, string(raw), "new_channel")
	assert.Contains(t, string(raw), "new_namespace")
}

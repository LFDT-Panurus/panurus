/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/config/mocks"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	TMSPath = "token.tms"
)

func TestNewService(t *testing.T) {
	cp := &mocks.Provider{}
	cp.GetStringReturns("v2")
	cp.GetBoolReturns(true)

	s := config.NewService(cp)
	assert.NotNil(t, s)
	assert.Equal(t, "v2", s.Version())
	assert.True(t, s.Enabled())

	// Test default version
	cp.GetStringReturns("")
	s = config.NewService(cp)
	assert.Equal(t, "v1", s.Version())
}

func TestLookupNamespace(t *testing.T) {
	cp := &mocks.Provider{}
	s := config.NewService(cp)

	// Test Case: Single Hit
	tmsID1 := driver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	tmsID2 := driver.TMSID{Network: "n2", Channel: "c2", Namespace: "ns2"}

	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == TMSPath {
			*rawVal.(*map[string]any) = map[string]any{
				"id1": nil,
				"id2": nil,
			}

			return nil
		}
		if key == "token.tms.id1" {
			*rawVal.(*driver.TMSID) = tmsID1

			return nil
		}
		if key == "token.tms.id2" {
			*rawVal.(*driver.TMSID) = tmsID2

			return nil
		}

		return nil
	}

	ns, err := s.LookupNamespace("n1", "c1")
	require.NoError(t, err)
	assert.Equal(t, "ns1", ns)

	// Test Case: No Hit
	ns, err = s.LookupNamespace("n3", "c3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no configuration")
	assert.Empty(t, ns)

	// Test Case: Multiple Hits
	tmsID3 := driver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns3"}
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == TMSPath {
			*rawVal.(*map[string]any) = map[string]any{
				"id1": nil,
				"id3": nil,
			}

			return nil
		}
		if key == "token.tms.id1" {
			*rawVal.(*driver.TMSID) = tmsID1

			return nil
		}
		if key == "token.tms.id3" {
			*rawVal.(*driver.TMSID) = tmsID3

			return nil
		}

		return nil
	}
	// We need to reset the holder to trigger a reload
	err = s.ResetConfigurations()
	require.NoError(t, err)

	_, err = s.LookupNamespace("n1", "c1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple configurations")
}

func TestConfigurationFor(t *testing.T) {
	cp := &mocks.Provider{}
	s := config.NewService(cp)

	tmsID1 := driver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == TMSPath {
			*rawVal.(*map[string]any) = map[string]any{
				"id1": nil,
			}

			return nil
		}
		if key == "token.tms.id1" {
			*rawVal.(*driver.TMSID) = tmsID1

			return nil
		}

		return nil
	}

	conf, err := s.ConfigurationFor("n1", "c1", "ns1")
	require.NoError(t, err)
	assert.NotNil(t, conf)
	assert.Equal(t, tmsID1, conf.ID())

	// Test Case: Not Found
	conf, err = s.ConfigurationFor("n2", "c2", "ns2")
	require.Error(t, err)
	assert.Nil(t, conf)
	assert.Contains(t, err.Error(), "configuration not found")
}

func TestAddConfiguration(t *testing.T) {
	cp := &mocks.Provider{}
	s := config.NewService(cp)

	raw := []byte("token:\n  tms:\n    new_id:\n      network: new\n      channel: new\n      namespace: new")

	// Test Case: ProvideFromRaw fails
	cp.ProvideFromRawReturns(nil, assert.AnError)
	err := s.AddConfiguration(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed loading configuration")

	// Test Case: Success
	vcp, err := fscconfig.NewProvider("./testdata/token0")
	require.NoError(t, err)
	err = vcp.MergeConfig(raw)
	require.NoError(t, err)

	cp.MergeConfigReturns(nil)
	cp.ProvideFromRawReturns(vcp, nil)
	err = s.AddConfiguration(raw)
	require.NoError(t, err)
	assert.Equal(t, 1, cp.MergeConfigCallCount())
	assert.Equal(t, raw, cp.MergeConfigArgsForCall(0))

	// Test Case: Already Exists
	// Initial state has "new" (id1 in previous tests, but let's use "new_id" here)
	cp.UnmarshalKeyStub = func(key string, rawVal any) error {
		if key == TMSPath {
			*rawVal.(*map[string]any) = map[string]any{
				"new_id": nil,
			}

			return nil
		}
		if key == "token.tms.new_id" {
			*rawVal.(*driver.TMSID) = driver.TMSID{Network: "new", Channel: "new", Namespace: "new"}

			return nil
		}

		return nil
	}
	err = s.ResetConfigurations()
	require.NoError(t, err)
	_, err = s.Configurations()
	require.NoError(t, err)

	cp.ProvideFromRawReturns(vcp, nil)
	err = s.AddConfiguration(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating existing configuration is not supported")
}

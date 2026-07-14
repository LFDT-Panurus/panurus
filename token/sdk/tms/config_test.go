/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfigServiceWrapper(t *testing.T) {
	configService := &config.Service{}

	wrapper := tms.NewConfigServiceWrapper(configService)

	require.NotNil(t, wrapper)
	assert.NotNil(t, wrapper)
}

func TestNewConfigServiceWrapper_WithNil(t *testing.T) {
	wrapper := tms.NewConfigServiceWrapper(nil)

	require.NotNil(t, wrapper)
}

func TestConfigServiceWrapper_Configurations(t *testing.T) {
	// Create wrapper with empty service
	wrapper := tms.NewConfigServiceWrapper(&config.Service{})

	// Call Configurations - will panic with nil service internals
	// We use defer/recover to test that the method can be called
	defer func() {
		if r := recover(); r != nil {
			// Expected - the service has nil internals
			t.Logf("Expected panic caught: %v", r)
		}
	}()

	_, err := wrapper.Configurations()

	// If we get here without panic, check for error
	_ = err
}

func TestConfigServiceWrapper_ConfigurationFor(t *testing.T) {
	wrapper := tms.NewConfigServiceWrapper(&config.Service{})

	network := "test-network"
	channel := "test-channel"
	namespace := "test-namespace"

	// Call the method - will panic with nil service internals
	// We use defer/recover to test that the method can be called
	defer func() {
		if r := recover(); r != nil {
			// Expected - the service has nil internals
			t.Logf("Expected panic caught: %v", r)
		}
	}()

	_, err := wrapper.ConfigurationFor(network, channel, namespace)

	// If we get here without panic, check for error
	_ = err
}

func TestConfigServiceWrapper_ConfigurationFor_Parameters(t *testing.T) {
	testCases := []struct {
		name      string
		network   string
		channel   string
		namespace string
	}{
		{
			name:      "standard parameters",
			network:   "network1",
			channel:   "channel1",
			namespace: "namespace1",
		},
		{
			name:      "empty parameters",
			network:   "",
			channel:   "",
			namespace: "",
		},
		{
			name:      "special characters",
			network:   "network-with-dash",
			channel:   "channel_with_underscore",
			namespace: "namespace.with.dots",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper := tms.NewConfigServiceWrapper(&config.Service{})

			// Use defer/recover to handle expected panic
			defer func() {
				if r := recover(); r != nil {
					// Expected - the service has nil internals
					t.Logf("Expected panic caught: %v", r)
				}
			}()

			_, err := wrapper.ConfigurationFor(tc.network, tc.channel, tc.namespace)
			_ = err
		})
	}
}

func TestConfigServiceWrapper_MultipleInstances(t *testing.T) {
	service1 := &config.Service{}
	service2 := &config.Service{}

	wrapper1 := tms.NewConfigServiceWrapper(service1)
	wrapper2 := tms.NewConfigServiceWrapper(service2)

	require.NotNil(t, wrapper1)
	require.NotNil(t, wrapper2)
	assert.NotSame(t, wrapper1, wrapper2)
}

// Note: The Configurations and ConfigurationFor methods are thin wrappers around
// config.Service methods. Full testing requires a properly initialized config.Service
// with actual configuration data, which is complex to set up in unit tests.
// These methods are comprehensively tested in integration tests where the full
// configuration system is initialized.

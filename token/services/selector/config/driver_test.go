/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/config/mock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Package config_test validates selector configuration parsing and default value handling.
// Tests cover: config loading, default values, getter methods, and error propagation.

func TestConfig_GetDriver(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected driver.Driver
	}{
		{
			name:     "default driver when empty",
			config:   &Config{},
			expected: driver.Sherdlock,
		},
		{
			name:     "custom driver",
			config:   &Config{Driver: driver.Simple},
			expected: driver.Simple,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetDriver()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetNumRetries(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected int
	}{
		{
			name:     "default when zero",
			config:   &Config{NumRetries: 0},
			expected: defaultNumRetries,
		},
		{
			name:     "custom value",
			config:   &Config{NumRetries: 5},
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetNumRetries()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetRetryInterval(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected time.Duration
	}{
		{
			name:     "default when zero",
			config:   &Config{RetryInterval: 0},
			expected: defaultRetryInterval,
		},
		{
			name:     "custom value",
			config:   &Config{RetryInterval: 10 * time.Second},
			expected: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetRetryInterval()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetLeaseExpiry(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected time.Duration
	}{
		{
			name:     "default when zero",
			config:   &Config{LeaseExpiry: 0},
			expected: defaultLeaseExpiry,
		},
		{
			name:     "custom value",
			config:   &Config{LeaseExpiry: 5 * time.Minute},
			expected: 5 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetLeaseExpiry()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetLeaseCleanupTickPeriod(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected time.Duration
	}{
		{
			name:     "default when zero",
			config:   &Config{LeaseCleanupTickPeriod: 0},
			expected: defaultLeaseCleanupTickPeriod,
		},
		{
			name:     "custom value",
			config:   &Config{LeaseCleanupTickPeriod: 2 * time.Minute},
			expected: 2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetLeaseCleanupTickPeriod()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetFetcherCacheSize(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected int64
	}{
		{
			name:     "returns zero when not set",
			config:   &Config{FetcherCacheSize: 0},
			expected: 0,
		},
		{
			name:     "returns custom value",
			config:   &Config{FetcherCacheSize: 500},
			expected: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetFetcherCacheSize()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetFetcherCacheRefresh(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected time.Duration
	}{
		{
			name:     "returns zero when not set",
			config:   &Config{FetcherCacheRefresh: 0},
			expected: 0,
		},
		{
			name:     "returns custom value",
			config:   &Config{FetcherCacheRefresh: 60 * time.Second},
			expected: 60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetFetcherCacheRefresh()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetFetcherCacheMaxQueries(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected int
	}{
		{
			name:     "returns zero when not set",
			config:   &Config{FetcherCacheMaxQueries: 0},
			expected: 0,
		},
		{
			name:     "returns custom value",
			config:   &Config{FetcherCacheMaxQueries: 200},
			expected: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetFetcherCacheMaxQueries()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNew verifies config parsing handles valid configs, empty configs, and unmarshal errors.
func TestNew(t *testing.T) {
	tests := []struct {
		name         string
		mockConfig   map[string]any
		unmarshalErr error
		expectError  bool
	}{
		{
			name: "successful config parsing",
			mockConfig: map[string]any{
				"token.selector": &Config{
					Driver:                 driver.Sherdlock,
					RetryInterval:          5 * time.Second,
					NumRetries:             3,
					FetcherCacheSize:       100,
					FetcherCacheRefresh:    30 * time.Second,
					FetcherCacheMaxQueries: 100,
				},
			},
			expectError: false,
		},
		{
			name:        "empty config",
			mockConfig:  map[string]any{},
			expectError: false,
		},
		{
			name:         "unmarshal error",
			mockConfig:   map[string]any{},
			unmarshalErr: errors.New("unmarshal failed"),
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mock.ConfigService{}
			mockSvc.UnmarshalKeyStub = func(key string, rawVal any) error {
				if tt.unmarshalErr != nil {
					return tt.unmarshalErr
				}
				if val, ok := tt.mockConfig[key]; ok {
					if c, ok := rawVal.(*Config); ok {
						if cfg, ok := val.(*Config); ok {
							*c = *cfg
						}
					}
				}

				return nil
			}

			cfg, err := New(mockSvc)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, cfg)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, cfg)
			}
		})
	}
}

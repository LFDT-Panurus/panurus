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
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
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

func TestConfig_GetRateLimit(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected int
	}{
		{
			// Rate limiting is off unless it is asked for: an unconfigured deployment must
			// see no limiter at all. The getter reports that as zero, which
			// ratelimit.FromConfig maps to a nil Limiter.
			name:     "off when not set",
			config:   &Config{},
			expected: 0,
		},
		{
			// rateLimitEnabled is the "protect me, pick the numbers for me" switch.
			name:     "enabled flag selects the built-in rate",
			config:   &Config{RateLimitEnabled: true},
			expected: ratelimit.DefaultRate,
		},
		{
			// A configured rate is never silently ignored: it switches the limiter on by
			// itself, without also needing the flag.
			name:     "custom value switches it on",
			config:   &Config{RateLimit: 42},
			expected: 42,
		},
		{
			name:     "custom value with the flag set",
			config:   &Config{RateLimitEnabled: true, RateLimit: 42},
			expected: 42,
		},
		{
			// A negative rate is the explicit "off" even when the flag says otherwise.
			name:     "negative value overrides the enabled flag",
			config:   &Config{RateLimitEnabled: true, RateLimit: -1},
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetRateLimit()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfig_GetRateLimitBurst(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected int
	}{
		{
			name:     "default when not set",
			config:   &Config{},
			expected: ratelimit.DefaultBurst,
		},
		{
			name:     "custom value",
			config:   &Config{RateLimitBurst: 7},
			expected: 7,
		},
		{
			name:     "default when not positive",
			config:   &Config{RateLimitBurst: -5},
			expected: ratelimit.DefaultBurst,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetRateLimitBurst()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// The built-in limiter reads its settings through the ratelimit.Config interface, so Config
// must keep satisfying it.
func TestConfig_SatisfiesRateLimitConfig(t *testing.T) {
	var _ ratelimit.Config = &Config{}

	limiter := ratelimit.FromConfig(&Config{RateLimit: 1, RateLimitBurst: 1})
	require.NotNil(t, limiter)
	t.Cleanup(limiter.Stop)

	require.NoError(t, limiter.Allow("alice"))
	require.Error(t, limiter.Allow("alice"))

	assert.Nil(t, ratelimit.FromConfig(&Config{RateLimit: -1}))
	// The zero config is the common case: no limiter.
	assert.Nil(t, ratelimit.FromConfig(&Config{}))
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
				// New always returns a non-nil *Config so callers that log and continue
				// get a safe zero-value config (all defaults) instead of a nil pointer.
				assert.NotNil(t, cfg, "cfg must be non-nil even on error")
			} else {
				require.NoError(t, err)
				assert.NotNil(t, cfg)
			}
		})
	}
}

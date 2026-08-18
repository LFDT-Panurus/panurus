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

// TestConfig_RateLimit verifies the built-in selection rate limiter is off unless it is enabled
// explicitly or implied by a positive rate, and that the documented defaults apply once it is on.
func TestConfig_RateLimit(t *testing.T) {
	tests := []struct {
		name            string
		config          *Config
		expectedEnabled bool
		expectedRate    float64
		expectedBurst   int
	}{
		{
			name:            "disabled by default",
			config:          &Config{},
			expectedEnabled: false,
			expectedRate:    0,
			expectedBurst:   0,
		},
		{
			name:            "enabled without values uses defaults",
			config:          &Config{RateLimitEnabled: true},
			expectedEnabled: true,
			expectedRate:    defaultRateLimit,
			expectedBurst:   defaultRateLimit * defaultRateLimitBurstFactor,
		},
		{
			name:            "positive rate implies enabled",
			config:          &Config{RateLimit: 20},
			expectedEnabled: true,
			expectedRate:    20,
			expectedBurst:   40,
		},
		{
			name:            "explicit burst is honoured",
			config:          &Config{RateLimit: 20, RateLimitBurst: 5},
			expectedEnabled: true,
			expectedRate:    20,
			expectedBurst:   5,
		},
		{
			name:            "burst without rate defaults the rate",
			config:          &Config{RateLimitEnabled: true, RateLimitBurst: 5},
			expectedEnabled: true,
			expectedRate:    defaultRateLimit,
			expectedBurst:   5,
		},
		{
			name:            "fractional rate rounds the burst up",
			config:          &Config{RateLimit: 0.5},
			expectedEnabled: true,
			expectedRate:    0.5,
			expectedBurst:   1,
		},
		{
			name:            "non-positive rate alone does not enable",
			config:          &Config{RateLimit: -1},
			expectedEnabled: false,
			expectedRate:    0,
			expectedBurst:   0,
		},
		{
			name:            "non-positive rate with explicit enable falls back to the default",
			config:          &Config{RateLimitEnabled: true, RateLimit: -1},
			expectedEnabled: true,
			expectedRate:    defaultRateLimit,
			expectedBurst:   defaultRateLimit * defaultRateLimitBurstFactor,
		},
		{
			name:            "non-positive burst falls back to the default",
			config:          &Config{RateLimit: 10, RateLimitBurst: -5},
			expectedEnabled: true,
			expectedRate:    10,
			expectedBurst:   20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedEnabled, tt.config.IsRateLimitEnabled())
			assert.InDelta(t, tt.expectedRate, tt.config.GetRateLimit(), 0)
			assert.Equal(t, tt.expectedBurst, tt.config.GetRateLimitBurst())
		})
	}
}

// TestConfig_GetRateLimitMaxBuckets verifies the bucket cap defaults to zero, which lets the
// limiter pick its own, and that a negative value is treated the same way.
func TestConfig_GetRateLimitMaxBuckets(t *testing.T) {
	assert.Equal(t, 0, (&Config{}).GetRateLimitMaxBuckets())
	assert.Equal(t, 0, (&Config{RateLimitMaxBuckets: -1}).GetRateLimitMaxBuckets())
	assert.Equal(t, 1024, (&Config{RateLimitMaxBuckets: 1024}).GetRateLimitMaxBuckets())
}

// TestConfig_ImplementsRateLimitConfiguration makes sure the parsed configuration keeps satisfying
// the interface the limiter builds itself from.
func TestConfig_ImplementsRateLimitConfiguration(t *testing.T) {
	var _ ratelimit.Configuration = &Config{}
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

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

//go:generate counterfeiter -o mock/config_provider.go -fake-name ConfigProvider . ConfigProvider

// ConfigProvider is a minimal interface for configuration access needed by LoadLockConfig.
// This interface allows for easy mocking in tests without depending on the full config.Configuration.
type ConfigProvider interface {
	// IsSet checks if a configuration key exists
	IsSet(key string) bool
	// UnmarshalKey unmarshals a configuration key into the provided struct
	UnmarshalKey(key string, rawVal any) error
}

const (
	// Default lock acquisition retry configuration constants
	defaultMaxLockRetries        = 10                    // Maximum number of retry attempts
	defaultInitialLockBackoff    = 10 * time.Millisecond // Initial backoff delay
	defaultMaxLockBackoff        = 5 * time.Second       // Maximum backoff delay
	defaultLockBackoffMultiplier = 2.0                   // Exponential backoff multiplier
	defaultLockJitterFactor      = 0.3                   // Randomization factor (30%)
)

// LockConfig holds the configuration for lock acquisition retry logic
type LockConfig struct {
	// MaxRetries is the maximum number of retry attempts for lock acquisition
	MaxRetries int
	// InitialBackoff is the initial backoff delay before the first retry
	InitialBackoff time.Duration
	// MaxBackoff is the maximum backoff delay between retries
	MaxBackoff time.Duration
	// BackoffMultiplier is the exponential backoff multiplier
	BackoffMultiplier float64
	// JitterFactor is the randomization factor to prevent thundering herd (0.0 to 1.0)
	JitterFactor float64
}

// DefaultLockConfig returns the default lock configuration
func DefaultLockConfig() *LockConfig {
	return &LockConfig{
		MaxRetries:        defaultMaxLockRetries,
		InitialBackoff:    defaultInitialLockBackoff,
		MaxBackoff:        defaultMaxLockBackoff,
		BackoffMultiplier: defaultLockBackoffMultiplier,
		JitterFactor:      defaultLockJitterFactor,
	}
}

// LockConfigRaw is used to unmarshal lock configuration from YAML
type LockConfigRaw struct {
	MaxRetries        int     `yaml:"maxRetries"`
	InitialBackoff    string  `yaml:"initialBackoff"`
	MaxBackoff        string  `yaml:"maxBackoff"`
	BackoffMultiplier float64 `yaml:"backoffMultiplier"`
	JitterFactor      float64 `yaml:"jitterFactor"`
}

// LoadLockConfig loads lock configuration from the configuration provider.
// If configuration is not found or invalid, returns default configuration.
func LoadLockConfig(cp ConfigProvider) *LockConfig {
	cfg := DefaultLockConfig()

	if !cp.IsSet("auditor.lock") {
		return cfg
	}

	var raw LockConfigRaw
	if err := cp.UnmarshalKey("auditor.lock", &raw); err != nil {
		logging.MustGetLogger().Warnf("failed to unmarshal auditor lock configuration, using defaults: %v", err)

		return cfg
	}

	// Apply max retries if valid
	if raw.MaxRetries > 0 {
		cfg.MaxRetries = raw.MaxRetries
	}

	// Apply initial backoff if valid
	if raw.InitialBackoff != "" {
		if duration, err := time.ParseDuration(raw.InitialBackoff); err == nil && duration > 0 {
			cfg.InitialBackoff = duration
		} else {
			logging.MustGetLogger().Warnf("invalid initialBackoff value [%s], using default", raw.InitialBackoff)
		}
	}

	// Apply max backoff if valid
	if raw.MaxBackoff != "" {
		if duration, err := time.ParseDuration(raw.MaxBackoff); err == nil && duration > 0 {
			cfg.MaxBackoff = duration
		} else {
			logging.MustGetLogger().Warnf("invalid maxBackoff value [%s], using default", raw.MaxBackoff)
		}
	}

	// Apply backoff multiplier if valid
	if raw.BackoffMultiplier > 0 {
		cfg.BackoffMultiplier = raw.BackoffMultiplier
	}

	// Apply jitter factor if valid (must be between 0 and 1)
	if raw.JitterFactor >= 0 && raw.JitterFactor <= 1.0 {
		cfg.JitterFactor = raw.JitterFactor
	}

	return cfg
}

// Adapter to make config.Configuration compatible with ConfigProvider interface
type configAdapter struct {
	*config.Configuration
}

func (c *configAdapter) IsSet(key string) bool {
	return c.Configuration.IsSet(key)
}

func (c *configAdapter) UnmarshalKey(key string, rawVal any) error {
	return c.Configuration.UnmarshalKey(key, rawVal)
}

// LoadLockConfigFromConfiguration is a convenience wrapper that adapts config.Configuration
// to the ConfigProvider interface and calls LoadLockConfig.
func LoadLockConfigFromConfiguration(cp *config.Configuration) *LockConfig {
	return LoadLockConfig(&configAdapter{cp})
}

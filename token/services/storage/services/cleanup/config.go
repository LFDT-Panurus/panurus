/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/config"
)

const (
	// ConfigKeyCleanup is the configuration key for keystore cleanup settings
	ConfigKeyCleanup = "services.storage.cleanup"
)

// Config holds the configuration for the keystore cleanup manager
type Config struct {
	// Enabled indicates whether keystore cleanup is enabled
	Enabled bool
	// TTL is the minimum age of deleted tokens before their keys are cleaned up
	TTL time.Duration
	// ScanInterval is how often to scan for deleted tokens needing cleanup
	ScanInterval time.Duration
	// BatchSize is the maximum number of tokens processed in a single sweep
	BatchSize int
	// WorkerCount is the number of local workers processing tokens
	WorkerCount int
	// InstanceID identifies the current replica as the cleanup owner
	InstanceID string
}

// DefaultConfig returns the default cleanup configuration
func DefaultConfig() Config {
	return Config{
		Enabled:      false,          // Disabled by default - must be explicitly enabled
		TTL:          24 * time.Hour, // Wait 24 hours before cleaning up keys
		ScanInterval: 1 * time.Hour,  // Scan every hour
		BatchSize:    100,
		WorkerCount:  1,
	}
}

// LoadConfig loads the cleanup configuration from the TMS configuration
func LoadConfig(cfg *config.Configuration) (Config, error) {
	// Start with defaults
	result := DefaultConfig()

	// Check if cleanup configuration exists
	if !cfg.IsSet(ConfigKeyCleanup) {
		return result, nil
	}

	// Unmarshal the cleanup configuration
	var config Config
	if err := cfg.UnmarshalKey(ConfigKeyCleanup, &config); err != nil {
		return result, err
	}

	// Apply configuration values (preserve defaults if not set)
	result.Enabled = config.Enabled
	if config.TTL > 0 {
		result.TTL = config.TTL
	}
	if config.ScanInterval > 0 {
		result.ScanInterval = config.ScanInterval
	}
	if config.BatchSize > 0 {
		result.BatchSize = config.BatchSize
	}
	if config.WorkerCount > 0 {
		result.WorkerCount = config.WorkerCount
	}
	if config.InstanceID != "" {
		result.InstanceID = config.InstanceID
	}
	// advisoryLockID used to let an operator keep multiple independent cleanup
	// managers on the same database from colliding. The lock id is now derived
	// per TMS automatically, so the key is silently ignored rather than failing
	// the load - warn so an operator who relied on it to isolate managers
	// notices the setting stopped doing anything, instead of finding out only
	// when leases start colliding.
	if cfg.IsSet(ConfigKeyCleanup + ".advisoryLockID") {
		logger.Warnf("%s.advisoryLockID is no longer used; the cleanup lock id is now derived per TMS", ConfigKeyCleanup)
	}

	return result, nil
}

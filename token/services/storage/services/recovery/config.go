/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/config"
)

const (
	// ConfigKeyRecovery is the configuration key for recovery settings
	ConfigKeyRecovery = "services.network.fabric.recovery"
)

// Config holds the configuration for the recovery manager
type Config struct {
	// Enabled indicates whether transaction recovery is enabled
	Enabled bool
	// TTL is the time-to-live for transactions before they are considered for recovery
	TTL time.Duration
	// ScanInterval is how often to scan for transactions needing recovery
	ScanInterval time.Duration
	// BatchSize is the maximum number of transactions claimed in a single sweep.
	BatchSize int
	// WorkerCount is the number of local workers processing claimed transactions.
	WorkerCount int
	// LeaseDuration is the duration of the recovery claim lease.
	LeaseDuration time.Duration
	// AdvisoryLockID is the PostgreSQL advisory lock ID used for leader election.
	AdvisoryLockID int64
	// InstanceID identifies the current replica as the lease owner.
	InstanceID string
	// NotFoundGracePeriod: when GetTransactionStatus returns a NotFound error and
	// the tx was stored more than this duration ago, the recovery loop marks the
	// row as Orphan instead of leaving it for another retry. Prevents the queue
	// from being permanently blocked by transactions that never reached the
	// ledger (broadcast failures, mempool drops). Zero disables this behaviour.
	NotFoundGracePeriod time.Duration
}

// DefaultConfig returns the default recovery configuration
func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		TTL:                 30 * time.Second,
		ScanInterval:        5 * time.Second,
		BatchSize:           defaultBatchSize,
		WorkerCount:         defaultWorkers,
		LeaseDuration:       defaultLeaseDuration,
		AdvisoryLockID:      defaultLockID,
		NotFoundGracePeriod: 30 * time.Minute,
	}
}

// LoadConfig loads the recovery configuration from the TMS configuration
func LoadConfig(cfg *config.Configuration) (Config, error) {
	// Start with defaults
	result := DefaultConfig()

	// Check if recovery configuration exists
	if !cfg.IsSet(ConfigKeyRecovery) {
		return result, nil
	}

	// Unmarshal the recovery configuration
	var config Config
	if err := cfg.UnmarshalKey(ConfigKeyRecovery, &config); err != nil {
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
	if config.LeaseDuration > 0 {
		result.LeaseDuration = config.LeaseDuration
	}
	if config.AdvisoryLockID != 0 {
		result.AdvisoryLockID = config.AdvisoryLockID
	}
	if config.InstanceID != "" {
		result.InstanceID = config.InstanceID
	}
	// NotFoundGracePeriod accepts an explicit zero to disable the Orphan
	// promotion, so check IsSet rather than the Go zero value. Without this
	// gate, setting notFoundGracePeriod: 0 in config would silently fall back
	// to the 30 min default and the documented opt-out would be unreachable.
	if cfg.IsSet(ConfigKeyRecovery + ".notFoundGracePeriod") {
		result.NotFoundGracePeriod = config.NotFoundGracePeriod
	}

	return result, nil
}

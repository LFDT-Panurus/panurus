/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/config"
	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
)

const (
	// ConfigKeyChecks is the configuration key for the ledger drift checks.
	ConfigKeyChecks = "services.storage.checks"
)

// Config holds the configuration of the ledger drift checks sweep.
type Config struct {
	// Enabled indicates whether the checks sweep runs. Default true: the checks
	// exist to be run, and a node that never runs them is the problem this service
	// was written for.
	Enabled bool
	// ScanInterval is how often a sweep runs.
	ScanInterval time.Duration
	// Timeout bounds one sweep. A sweep that runs past it is cancelled, and the
	// next one starts from scratch on the following interval.
	Timeout time.Duration
	// BatchSize is how many tokens a check resolves against the ledger per round trip.
	BatchSize int
	// TransactionWindow restricts the checks that walk the transaction history to
	// transactions stored within this duration of now. Zero, the default, means the
	// whole history.
	//
	// Narrowing it bounds the cost of a sweep on a node with a long history, but it
	// also means those checks no longer see everything: their findings are then
	// never closed automatically, because a sweep that did not look at a
	// transaction has learned nothing about it.
	TransactionWindow time.Duration
}

// DefaultConfig returns the default checks configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:           true,
		ScanInterval:      time.Hour,
		Timeout:           30 * time.Minute,
		BatchSize:         dbcommon.DefaultBatchSize,
		TransactionWindow: 0,
	}
}

// LoadConfig loads the checks configuration from the TMS configuration, falling
// back to DefaultConfig for anything it does not set.
func LoadConfig(cfg *config.Configuration) (Config, error) {
	result := DefaultConfig()

	if !cfg.IsSet(ConfigKeyChecks) {
		return result, nil
	}

	var loaded Config
	if err := cfg.UnmarshalKey(ConfigKeyChecks, &loaded); err != nil {
		return result, err
	}

	// Enabled accepts an explicit false to disable the sweep, so check IsSet
	// rather than the Go zero value. Without this gate, a checks: block that
	// only tunes another field (e.g. scanInterval) would silently disable the
	// sweep, contradicting the documented default of true.
	if cfg.IsSet(ConfigKeyChecks + ".enabled") {
		result.Enabled = loaded.Enabled
	}
	if loaded.ScanInterval > 0 {
		result.ScanInterval = loaded.ScanInterval
	}
	if loaded.Timeout > 0 {
		result.Timeout = loaded.Timeout
	}
	if loaded.BatchSize > 0 {
		result.BatchSize = loaded.BatchSize
	}
	if loaded.TransactionWindow > 0 {
		result.TransactionWindow = loaded.TransactionWindow
	}

	return result, nil
}

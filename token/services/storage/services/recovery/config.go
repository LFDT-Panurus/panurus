/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// ConfigKeyRecovery is the configuration key for recovery settings
	ConfigKeyRecovery = "services.network.fabric.recovery"

	defaultStuckTransactionAlertThreshold = 5
)

var logger = logging.MustGetLogger()

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
	// TransactionTimeout bounds a single transaction's recovery attempt (the
	// handler's Recover call, which queries the ledger for status and applies
	// finality logic). Without it, a hung status query blocks the worker
	// indefinitely and, because leadership is held for the whole sweep, stalls
	// recovery on every replica of the TMS. An attempt that outlives this
	// deadline keeps its claim rather than releasing it (see manager.go's
	// finishAbandonedRecovery), so Start() only Warns, it does not fail,
	// when leaseDuration > (batchSize/workerCount) x transactionTimeout or
	// scanInterval < leaseDuration do not hold — see warnIfLeaseMayExpireMidSweep
	// and warnIfLeaseMayExpireBetweenSweeps. Zero disables the deadline
	// (accepts explicit zero as "unbounded", see LoadConfig); validateConfig
	// rejects negative values. LoadConfig separately rejects an operator's
	// explicit non-zero value below a 10s floor: a shorter deadline reads a
	// slow-but-legitimate ledger round-trip under load as a stuck transaction
	// and abandons it before it had a real chance to finish. That floor is
	// enforced in LoadConfig rather than validateConfig because it only
	// applies to a value the operator actually chose, not one this package
	// defaulted or clamped to, and LoadConfig is the only place that still
	// knows which is which (see LoadConfig's TransactionTimeout handling).
	TransactionTimeout time.Duration
	// StuckTransactionAlertThreshold: after this many consecutive
	// TransactionTimeout expirations for the same transaction, the recovery
	// loop logs at Error instead of Warn so a persistently-timing-out
	// transaction is visible to alerting rather than blending into the
	// routine per-sweep failure log. It never changes the transaction's
	// stored status: a timeout only means the status query didn't answer in
	// time, not that the transaction failed to reach the ledger, so unlike
	// a persistent NotFound it cannot be promoted to Orphan. Zero disables
	// the escalation.
	StuckTransactionAlertThreshold int
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
		Enabled:                        true,
		TTL:                            30 * time.Second,
		ScanInterval:                   5 * time.Second,
		BatchSize:                      defaultBatchSize,
		WorkerCount:                    defaultWorkers,
		LeaseDuration:                  defaultLeaseDuration,
		TransactionTimeout:             defaultTransactionTimeout,
		NotFoundGracePeriod:            30 * time.Minute,
		StuckTransactionAlertThreshold: defaultStuckTransactionAlertThreshold,
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
	// TransactionTimeout accepts an explicit zero to mean "unbounded" (opt out
	// of the per-transaction deadline entirely), so check IsSet rather than
	// the Go zero value, same reasoning as NotFoundGracePeriod below. Without
	// this gate, transactionTimeout: 0 would silently fall back to the
	// package default and the opt-out would be unreachable.
	if cfg.IsSet(ConfigKeyRecovery + ".transactionTimeout") {
		if config.TransactionTimeout > 0 && config.TransactionTimeout < minTransactionTimeout {
			return Config{}, errors.Errorf("recovery transactionTimeout [%s] is below the %s floor: a shorter deadline risks abandoning recoveries that were about to succeed", config.TransactionTimeout, minTransactionTimeout)
		}
		result.TransactionTimeout = config.TransactionTimeout
	} else if result.TransactionTimeout >= result.LeaseDuration {
		// The operator never touched transactionTimeout, so it is still
		// sitting at the package default here. If they configured a smaller
		// leaseDuration, a transactionTimeout that size makes little
		// operational sense next to it (round 7 no longer hard-fails Start()
		// over this specific relationship, see warnIfLeaseMayExpireMidSweep,
		// but there is still no reason to leave an operator at a default this
		// disproportionate to a lease they explicitly chose). Clamp the
		// default down instead; a transactionTimeout the operator set
		// explicitly is left exactly as they wrote it.
		//
		// Floored at minTransactionTimeout rather than left at
		// leaseDuration/2: the explicit-value floor check above only fires
		// when IsSet is true, so a clamped default landing below it would
		// otherwise slip through unrejected, and 0 here would silently land
		// the operator in the "unbounded" opt-out (see the IsSet gate above
		// and callHandler) rather than the bounded default this clamp exists
		// to keep them on. For a leaseDuration small enough that half of it
		// undercuts the floor, the clamped default ends up at or above
		// leaseDuration itself; that is exactly the disproportionate
		// relationship warnIfLeaseMayExpireMidSweep and
		// warnIfLeaseMayExpireBetweenSweeps Warn about at Start(), which is
		// the right severity for a lease this small, not a hard failure.
		result.TransactionTimeout = max(result.LeaseDuration/2, minTransactionTimeout)
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
	// StuckTransactionAlertThreshold accepts an explicit zero to disable the
	// log escalation, so check IsSet rather than the Go zero value, same as
	// NotFoundGracePeriod above.
	if cfg.IsSet(ConfigKeyRecovery + ".stuckTransactionAlertThreshold") {
		result.StuckTransactionAlertThreshold = config.StuckTransactionAlertThreshold
	}
	// advisoryLockID used to let an operator keep multiple independent recovery
	// managers on the same database from colliding. The lock id is now derived
	// per TMS automatically, so the key is silently ignored rather than failing
	// the load - warn so an operator who relied on it to isolate managers
	// notices the setting stopped doing anything, instead of finding out only
	// when leases start colliding.
	if cfg.IsSet(ConfigKeyRecovery + ".advisoryLockID") {
		logger.Warnf("%s.advisoryLockID is no longer used; the recovery lock id is now derived per TMS", ConfigKeyRecovery)
	}

	return result, nil
}

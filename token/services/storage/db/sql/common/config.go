/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

const (
	// ConfigKeyTableNames is the absolute configuration key for global SQL table name overrides.
	//
	// This key is resolved against the root configuration provider (not a TMS-scoped
	// configuration), so it applies to all TMS instances on the node and maps directly
	// to token.storage.tableNames in the configuration file.
	//
	// Example YAML:
	//
	//   token:
	//     storage:
	//       tableNames:
	//         id_signers: identity_signers
	//         tokens: my_tokens
	ConfigKeyTableNames = "token.storage.tableNames"

	// ConfigKeySkipPrefix is the absolute configuration key for disabling the
	// FSC-generated prefix on SQL table names.
	//
	// When set to true, table names are produced without any prefix, regardless
	// of what prefix is supplied at runtime. Default is false.
	//
	// Example YAML:
	//
	//   token:
	//     storage:
	//       skipPrefix: true
	ConfigKeySkipPrefix = "token.storage.skipPrefix"

	// ConfigKeyLockStrategy is the absolute configuration key for the
	// token-lock acquisition strategy used by SQL backends that support more
	// than one (currently Postgres only; other backends read and ignore it).
	//
	// Example YAML:
	//
	//   token:
	//     storage:
	//       db:
	//         lockStrategy: skipLocked
	ConfigKeyLockStrategy = "token.storage.db.lockStrategy"

	// LockStrategyInsert is the default lock strategy: INSERT and catch the
	// unique-constraint violation on a lost race. Equivalent to leaving
	// ConfigKeyLockStrategy unset.
	LockStrategyInsert = "insert"
	// LockStrategyOnConflict acquires a lock with
	// INSERT ... ON CONFLICT DO NOTHING RETURNING, avoiding a server-side
	// unique-violation error on a lost race.
	LockStrategyOnConflict = "onConflict"
	// LockStrategySkipLocked behaves like LockStrategyOnConflict for a single
	// lock, and additionally allows a covering-window batch claim using
	// FOR UPDATE SKIP LOCKED to avoid colliding with rows a concurrent
	// claimant is already processing.
	LockStrategySkipLocked = "skipLocked"

	// ConfigKeyPoolMetricsInterval is the absolute configuration key for how
	// often the Postgres connection-pool sampler reads (*sql.DB).Stats() and
	// updates db_pool_in_use/db_pool_max_open/db_pool_wait_*. Unlike its
	// siblings above, this key is observability only: it changes nothing about
	// how storage operates, only how often it is reported on. Default is 1s;
	// 0 disables the sampler (and its goroutine) entirely, for an operator who
	// does not scrape Prometheus.
	//
	// Example YAML:
	//
	//   token:
	//     storage:
	//       db:
	//         poolMetricsInterval: 1s
	ConfigKeyPoolMetricsInterval = "token.storage.db.poolMetricsInterval"

	// DefaultPoolMetricsInterval is used when ConfigKeyPoolMetricsInterval is
	// unset. 1s rather than a coarser interval because the signal it exists to
	// catch - a pool held near-saturated by connections with a short average
	// hold time, such as the ~250ms settlement hold in #2474 - would be missed
	// most of the time by a slower poll: at 10s the duty cycle is roughly
	// 1-in-40, at 1s roughly 1-in-4, and a *sustained* exhaustion episode (the
	// kind that actually hangs Select calls) is sampled reliably either way.
	// Burst detection does not depend on this interval: db_pool_wait_count_total
	// increments once per blocked checkout regardless of how often it is
	// scraped, so the gauge's job is occupancy context, not detection.
	DefaultPoolMetricsInterval = time.Second
)

// TableNamesConfig maps a short code (e.g. "id_signers") to the replacement short code
// that will be used when generating the final SQL table name. The FSC-generated prefix
// and params are still applied around the replacement value.
//
// Unknown keys are warned and ignored. Omitted keys keep their default short codes.
type TableNamesConfig map[string]string

// StorageConfig holds all SQL storage configuration options that can be read
// from the configuration provider.
type StorageConfig struct {
	// TableNames contains optional short-code overrides for individual table names.
	TableNames TableNamesConfig
	// SkipPrefix disables the FSC-generated prefix on all table names when true.
	// Default is false.
	SkipPrefix bool
	// LockStrategy selects the token-lock acquisition strategy. One of
	// LockStrategyInsert (default), LockStrategyOnConflict, or
	// LockStrategySkipLocked. Only acted upon by the Postgres driver.
	LockStrategy string
	// PoolMetricsInterval is how often the connection-pool sampler reads
	// (*sql.DB).Stats(). Defaults to DefaultPoolMetricsInterval; 0 disables
	// the sampler. Only acted upon by the Postgres driver.
	PoolMetricsInterval time.Duration
}

// LoadTableNamesConfig reads the table name overrides from cfg.
// It returns an empty map (and nil error) when cfg is nil or the config key is absent.
func LoadTableNamesConfig(cfg driver2.Config) (TableNamesConfig, error) {
	if cfg == nil || !cfg.IsSet(ConfigKeyTableNames) {
		return TableNamesConfig{}, nil
	}

	var result TableNamesConfig
	if err := cfg.UnmarshalKey(ConfigKeyTableNames, &result); err != nil {
		return TableNamesConfig{}, err
	}

	return result, nil
}

// LoadStorageConfig reads all SQL storage options from cfg.
// It returns a zero-value StorageConfig (and nil error) when cfg is nil.
//
// On a validation error for one option, it still returns every option parsed successfully so
// far (with LockStrategy defaulted to LockStrategyInsert), rather than discarding them. Callers
// that warn-and-continue on error (e.g. postgres/driver.go, sqlite/driver.go) otherwise silently
// lose TableNames/SkipPrefix to an unrelated typo in lockStrategy - see #2395 Phase 7.
func LoadStorageConfig(cfg driver2.Config) (StorageConfig, error) {
	tableNames, err := LoadTableNamesConfig(cfg)
	if err != nil {
		return StorageConfig{}, err
	}

	var skipPrefix bool
	if cfg != nil && cfg.IsSet(ConfigKeySkipPrefix) {
		if err := cfg.UnmarshalKey(ConfigKeySkipPrefix, &skipPrefix); err != nil {
			return StorageConfig{TableNames: tableNames}, err
		}
	}

	lockStrategy := LockStrategyInsert
	if cfg != nil && cfg.IsSet(ConfigKeyLockStrategy) {
		if err := cfg.UnmarshalKey(ConfigKeyLockStrategy, &lockStrategy); err != nil {
			return StorageConfig{TableNames: tableNames, SkipPrefix: skipPrefix, LockStrategy: LockStrategyInsert, PoolMetricsInterval: DefaultPoolMetricsInterval}, err
		}
		switch lockStrategy {
		case LockStrategyInsert, LockStrategyOnConflict, LockStrategySkipLocked:
		default:
			return StorageConfig{TableNames: tableNames, SkipPrefix: skipPrefix, LockStrategy: LockStrategyInsert, PoolMetricsInterval: DefaultPoolMetricsInterval}, errors.Errorf(
				"invalid value [%s] for [%s]: must be one of [%s, %s, %s]",
				lockStrategy, ConfigKeyLockStrategy, LockStrategyInsert, LockStrategyOnConflict, LockStrategySkipLocked,
			)
		}
	}

	poolMetricsInterval := DefaultPoolMetricsInterval
	if cfg != nil && cfg.IsSet(ConfigKeyPoolMetricsInterval) {
		raw := DefaultPoolMetricsInterval.String()
		if err := cfg.UnmarshalKey(ConfigKeyPoolMetricsInterval, &raw); err != nil {
			return StorageConfig{TableNames: tableNames, SkipPrefix: skipPrefix, LockStrategy: lockStrategy, PoolMetricsInterval: DefaultPoolMetricsInterval}, err
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return StorageConfig{TableNames: tableNames, SkipPrefix: skipPrefix, LockStrategy: lockStrategy, PoolMetricsInterval: DefaultPoolMetricsInterval}, errors.Wrapf(
				err, "invalid value [%s] for [%s]: must be a Go duration", raw, ConfigKeyPoolMetricsInterval,
			)
		}
		if parsed < 0 {
			return StorageConfig{TableNames: tableNames, SkipPrefix: skipPrefix, LockStrategy: lockStrategy, PoolMetricsInterval: DefaultPoolMetricsInterval}, errors.Errorf(
				"invalid value [%s] for [%s]: must not be negative", raw, ConfigKeyPoolMetricsInterval,
			)
		}
		poolMetricsInterval = parsed
	}

	return StorageConfig{
		TableNames:          tableNames,
		SkipPrefix:          skipPrefix,
		LockStrategy:        lockStrategy,
		PoolMetricsInterval: poolMetricsInterval,
	}, nil
}

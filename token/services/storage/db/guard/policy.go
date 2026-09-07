/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package guard provides a stackable decorator layer over the storage-service
// stores. It enforces cross-cutting resource limits — a maximum write payload
// size and a maximum number of rows a single read may return — without the
// concrete SQL stores having to know about them.
//
// Decorators embed the driver store interface (so every method delegates by
// default) and override only the methods that need a check, plus the nested
// transaction objects and returned iterators. They are applied once, at the
// multiplexed driver seam, driven by a single Policy loaded from configuration.
package guard

import (
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver"
)

const (
	// DefaultMaxPayloadSize is the default maximum write payload size in bytes
	// (0 disables the check).
	DefaultMaxPayloadSize = 4 << 20 // 4 MiB
	// DefaultMaxPageSize is the default maximum number of rows a single read may return.
	DefaultMaxPageSize = 1000
)

const (
	// ConfigKeyMaxPayloadSize is the config key for the max write size in bytes
	// (0 disables). Absent means use the default.
	ConfigKeyMaxPayloadSize = "token.storage.maxPayloadSize"

	// ConfigKeyMaxPageSize is the config key for the max rows a single read may
	// return. Absent means use the default.
	ConfigKeyMaxPageSize = "token.storage.maxPageSize"
)

// Policy holds the resource limits enforced by the guard decorators.
type Policy struct {
	// MaxPayloadSize is the max write size in bytes; 0 disables the check.
	MaxPayloadSize int
	// MaxPageSize is the max rows a single read may return; 0 disables the cap.
	MaxPageSize int
}

// DefaultPolicy returns the built-in limits.
func DefaultPolicy() Policy {
	return Policy{MaxPayloadSize: DefaultMaxPayloadSize, MaxPageSize: DefaultMaxPageSize}
}

// LoadPolicy reads the limits from cfg, falling back to the defaults for any
// key that is absent. An explicit value (including 0) overrides the default,
// so operators can disable a check by setting the key to 0.
func LoadPolicy(cfg driver.Config) (Policy, error) {
	p := DefaultPolicy()

	maxPayloadSize, err := loadOptionalInt(cfg, ConfigKeyMaxPayloadSize)
	if err != nil {
		return Policy{}, err
	}
	if maxPayloadSize != nil {
		p.MaxPayloadSize = *maxPayloadSize
	}

	maxPageSize, err := loadOptionalInt(cfg, ConfigKeyMaxPageSize)
	if err != nil {
		return Policy{}, err
	}
	if maxPageSize != nil {
		p.MaxPageSize = *maxPageSize
	}

	return p, nil
}

// loadOptionalInt reads an int config value, returning nil when cfg is nil or
// the key is absent so callers can tell "unset" from a set 0.
func loadOptionalInt(cfg driver.Config, key string) (*int, error) {
	if cfg == nil || !cfg.IsSet(key) {
		return nil, nil
	}
	var v int
	if err := cfg.UnmarshalKey(key, &v); err != nil {
		return nil, err
	}

	return &v, nil
}

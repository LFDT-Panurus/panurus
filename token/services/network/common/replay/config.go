/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package replay

import "time"

// Backend identifies which Guard implementation to use.
type Backend string

const (
	// BackendMemory selects the in-memory Guard. See replay/memory.
	BackendMemory Backend = "memory"
)

// Config is the configuration for a replay Guard.
type Config struct {
	// Backend selects the Guard implementation. Defaults to BackendMemory.
	Backend Backend `yaml:"backend"`
	// Window bounds how far a key's claimed Timestamp may lie from the guard's current time,
	// in either direction, before it is rejected with ErrOutOfWindow. The window moves with
	// the guard's clock. Window <= 0 disables the freshness check.
	Window time.Duration `yaml:"window"`
	// TTL is how long a seen key is remembered before it can be forgotten. Only meaningful
	// for backends whose entries expire (e.g. BackendMemory). Must be at least 2*Window so an
	// entry survives its entire potential-replay lifetime; backends enforce this floor.
	TTL time.Duration `yaml:"ttl"`
	// MaxEntries caps the number of keys remembered at once (0 means unbounded). Only
	// meaningful for backends with a bounded size (e.g. BackendMemory).
	MaxEntries int `yaml:"maxEntries"`
}

// DefaultConfig returns the configuration used when none is explicitly set: an in-memory
// guard with a 5-minute freshness window, remembering a key for 10 minutes, bounded to
// 100000 entries.
func DefaultConfig() Config {
	return Config{
		Backend:    BackendMemory,
		Window:     5 * time.Minute,
		TTL:        10 * time.Minute,
		MaxEntries: 100_000,
	}
}

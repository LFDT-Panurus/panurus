/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

// ConfiguredIdentity describes an identity entry parsed from configuration.
//
// Fields:
//   - ID: the unique identifier for the identity
//   - Default: whether this identity should be considered the default
//   - Path: file-system path or location where credentials/configuration live
//   - CacheSize: per-identity cache size. 0 disables the cache, nil falls back to
//     wallets.defaultCacheSize
//   - Opts: provider-specific options (opaque)
type ConfiguredIdentity struct {
	ID        string `yaml:"id"`
	Default   bool   `yaml:"default,omitempty"`
	Path      string `yaml:"path"`
	CacheSize *int   `yaml:"cacheSize,omitempty"`
	Opts      any    `yaml:"opts,omitempty"`
}

// String returns the identity's ID and satisfies fmt.Stringer.
func (i *ConfiguredIdentity) String() string {
	return i.ID
}

// Config is a read-only view over identity service configuration.
// Implementors provide lookup helpers used by the identity service to resolve configured
// identities and to normalize configured paths.
//
//go:generate counterfeiter -o mock/config.go -fake-name Config . Config
type Config interface {
	// CacheSizeForOwnerID returns the cache size to be used for the given owner wallet.
	// A return value of 0 disables the cache.
	CacheSizeForOwnerID(id string) int

	// TranslatePath maps a configured relative or templated path to an absolute
	// runtime path. Implementations should resolve variables and perform any
	// environment-specific transformations required to locate credential files.
	TranslatePath(path string) string
}

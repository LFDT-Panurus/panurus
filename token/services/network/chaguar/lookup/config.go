/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup

import "time"

const (
	// PermanentInterval is the configuration key for the polling interval of permanent lookups
	PermanentInterval = "token.chaguar.lookup.permanent.interval"
	// OnceDeadline is the configuration key for the deadline of one-time lookups
	OnceDeadline = "token.chaguar.lookup.once.deadline"
	// OnceInterval is the configuration key for the polling interval of one-time lookups
	OnceInterval = "token.chaguar.lookup.once.interval"

	// shortcut: the token.fabricx.lookup.* keys were renamed to token.chaguar.lookup.*;
	// they are still read when the current key is unset, drop them once deployments migrated.
	deprecatedPermanentInterval = "token.fabricx.lookup.permanent.interval"
	deprecatedOnceDeadline      = "token.fabricx.lookup.once.deadline"
	deprecatedOnceInterval      = "token.fabricx.lookup.once.interval"

	// DefaultPermanentInterval is the default polling interval for permanent lookups
	DefaultPermanentInterval = 1 * time.Minute
	// DefaultOnceDeadline is the default deadline for one-time lookups
	DefaultOnceDeadline = 5 * time.Minute
	// DefaultOnceInterval is the default polling interval for one-time lookups
	DefaultOnceInterval = 2 * time.Second
)

// ConfigGetter models the configuration getter for the lookup service
type ConfigGetter interface {
	// PermanentInterval returns the polling interval for permanent lookups
	PermanentInterval() time.Duration
	// OnceDeadline returns the deadline for one-time lookups
	OnceDeadline() time.Duration
	// OnceInterval returns the polling interval for one-time lookups
	OnceInterval() time.Duration
}

// Configuration models the configuration for the lookup service.
//
//go:generate counterfeiter -o mock/configuration.go -fake-name Configuration . Configuration
type Configuration interface {
	// GetDuration returns the duration for the given key.
	GetDuration(key string) time.Duration
}

// NewConfig creates a new ConfigGetter instance that uses the provided
// Configuration interface to retrieve lookup service settings.
func NewConfig(configuration Configuration) *serviceConfig {
	return &serviceConfig{configuration: configuration}
}

type serviceConfig struct {
	configuration Configuration
}

// duration returns the duration configured under key. A value not greater than 0
// falls back to the deprecated key, and then to def.
func (c *serviceConfig) duration(key, deprecated string, def time.Duration) time.Duration {
	if v := c.configuration.GetDuration(key); v > 0 {
		return v
	}
	if v := c.configuration.GetDuration(deprecated); v > 0 {
		logger.Warningf("configuration key [%s] is deprecated, use [%s] instead", deprecated, key)

		return v
	}

	return def
}

// PermanentInterval returns the polling interval for permanent lookups.
// It defaults to 1 minute.
func (c *serviceConfig) PermanentInterval() time.Duration {
	return c.duration(PermanentInterval, deprecatedPermanentInterval, DefaultPermanentInterval)
}

// OnceDeadline returns the maximum time allowed for a one-time lookup.
// It defaults to 5 minutes.
func (c *serviceConfig) OnceDeadline() time.Duration {
	return c.duration(OnceDeadline, deprecatedOnceDeadline, DefaultOnceDeadline)
}

// OnceInterval returns the polling interval for one-time lookups.
// It defaults to 2 seconds.
func (c *serviceConfig) OnceInterval() time.Duration {
	return c.duration(OnceInterval, deprecatedOnceInterval, DefaultOnceInterval)
}

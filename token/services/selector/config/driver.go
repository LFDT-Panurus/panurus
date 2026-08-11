/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/driver"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	defaultDriver                 = driver.Sherdlock
	defaultLeaseExpiry            = 3 * time.Minute
	defaultLeaseCleanupTickPeriod = 1 * time.Minute
	defaultNumRetries             = 3
	defaultRetryInterval          = 5 * time.Second
	defaultFetcherCacheSize       = 0 // 0 means use fetcher default
	defaultFetcherCacheRefresh    = 0 // 0 means use fetcher default
	defaultFetcherCacheMaxQueries = 0 // 0 means use fetcher default
	defaultRateLimit              = ratelimit.DefaultRate
	defaultRateLimitBurst         = ratelimit.DefaultBurst
	// rateLimitOff is what GetRateLimit reports when rate limiting is not switched on,
	// which is the default.
	rateLimitOff = 0
)

//go:generate counterfeiter -o mock/config_service.go -fake-name ConfigService . configService
type configService interface {
	UnmarshalKey(key string, rawVal any) error
}

type Config struct {
	Driver                 driver.Driver `yaml:"driver,omitempty"`
	RetryInterval          time.Duration `yaml:"retryInterval,omitempty"`
	NumRetries             int           `yaml:"numRetries,omitempty"`
	LeaseExpiry            time.Duration `yaml:"leaseExpiry,omitempty"`
	LeaseCleanupTickPeriod time.Duration `yaml:"leaseCleanupTickPeriod,omitempty"`
	FetcherCacheSize       int64         `yaml:"fetcherCacheSize,omitempty"`
	FetcherCacheRefresh    time.Duration `yaml:"fetcherCacheRefresh,omitempty"`
	FetcherCacheMaxQueries int           `yaml:"fetcherCacheMaxQueries,omitempty"`
	// RateLimitEnabled switches the built-in per-wallet rate limiter on with its built-in
	// rate and burst, for deployments that want basic protection without picking numbers.
	// Rate limiting is off unless this is true or RateLimit is set.
	RateLimitEnabled bool `yaml:"rateLimitEnabled,omitempty"`
	// RateLimit is the number of token-selection requests a single wallet may perform per
	// second before being throttled. A positive value switches the built-in limiter on with
	// that rate; a negative value keeps it off even when RateLimitEnabled is true. Zero
	// (unset) leaves the decision to RateLimitEnabled.
	RateLimit int `yaml:"rateLimit,omitempty"`
	// RateLimitBurst is the largest burst of selections a single wallet may perform after
	// an idle period. Zero (unset) selects the built-in default. Values below RateLimit are
	// raised to it.
	RateLimitBurst int `yaml:"rateLimitBurst,omitempty"`
}

// New returns a Config populated from the token.selector key. On unmarshal failure it
// returns a zero-value &Config{} (all defaults) together with the error, so callers that
// log the error and continue receive a safe, fully-functional config rather than a nil
// pointer.
func New(config configService) (*Config, error) {
	c := &Config{}
	if err := config.UnmarshalKey("token.selector", c); err != nil {
		return c, errors.Wrap(err, "invalid config for key [token.selector]: expected retryInterval (duration) and numRetries (integer))")
	}

	return c, nil
}

func (c *Config) GetDriver() driver.Driver {
	if c.Driver == "" {
		return defaultDriver
	}

	return c.Driver
}

func (c *Config) GetNumRetries() int {
	if c.NumRetries > 0 {
		return c.NumRetries
	}

	return defaultNumRetries
}

func (c *Config) GetRetryInterval() time.Duration {
	if c.RetryInterval != 0 {
		return c.RetryInterval
	}

	return defaultRetryInterval
}

func (c *Config) GetLeaseExpiry() time.Duration {
	if c.LeaseExpiry != 0 {
		return c.LeaseExpiry
	}

	return defaultLeaseExpiry
}

func (c *Config) GetLeaseCleanupTickPeriod() time.Duration {
	if c.LeaseCleanupTickPeriod != 0 {
		return c.LeaseCleanupTickPeriod
	}

	return defaultLeaseCleanupTickPeriod
}

func (c *Config) GetFetcherCacheSize() int64 {
	// Return 0 if not set, which will trigger use of fetcher default
	return c.FetcherCacheSize
}

func (c *Config) GetFetcherCacheRefresh() time.Duration {
	// Return 0 if not set, which will trigger use of fetcher default
	return c.FetcherCacheRefresh
}

func (c *Config) GetFetcherCacheMaxQueries() int {
	// Return 0 if not set, which will trigger use of fetcher default
	return c.FetcherCacheMaxQueries
}

// GetRateLimit returns the number of selections per second per wallet the built-in limiter
// allows, or zero when rate limiting is off. Off is the default: it takes either a positive
// RateLimit or RateLimitEnabled to switch the limiter on.
func (c *Config) GetRateLimit() int {
	// An explicit rate decides on its own, in both directions: positive switches the
	// limiter on with that rate, negative keeps it off.
	if c.RateLimit != 0 {
		return c.RateLimit
	}
	if c.RateLimitEnabled {
		return defaultRateLimit
	}

	return rateLimitOff
}

// GetRateLimitBurst returns the bucket capacity for the built-in limiter.
func (c *Config) GetRateLimitBurst() int {
	if c.RateLimitBurst > 0 {
		return c.RateLimitBurst
	}

	return defaultRateLimitBurst
}

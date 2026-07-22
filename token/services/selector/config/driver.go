/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/driver"
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
	// defaultRateLimit is the built-in default rate limit, in selection requests
	// per second per wallet. The limiter is on by default with this value.
	defaultRateLimit = 10.0
	// defaultRateLimitBurst is the built-in default burst capacity per wallet.
	defaultRateLimitBurst = 20.0
	// defaultRateLimitIdleTTL is how long a per-wallet bucket may be idle before
	// it is evicted to reclaim memory.
	defaultRateLimitIdleTTL = 10 * time.Minute
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
	// RateLimit is the built-in rate limiter's allowed selection requests per
	// second per wallet. Semantics: unset/0 uses the built-in default
	// (defaultRateLimit) and the limiter is enabled; a negative value disables
	// the built-in limiter entirely.
	RateLimit float64 `yaml:"rateLimit,omitempty"`
	// RateLimitBurst is the built-in rate limiter's burst capacity per wallet.
	// Unset/<=0 uses the built-in default (defaultRateLimitBurst).
	RateLimitBurst float64 `yaml:"rateLimitBurst,omitempty"`
	// RateLimitIdleTTL is how long a per-wallet bucket may be idle before it is
	// evicted. Unset/<=0 uses the built-in default (defaultRateLimitIdleTTL).
	RateLimitIdleTTL time.Duration `yaml:"rateLimitIdleTTL,omitempty"`
}

// New returns a SelectorConfig with the values from the token.selector key
func New(config configService) (*Config, error) {
	c := &Config{}
	err := config.UnmarshalKey("token.selector", c)
	if err != nil {
		return nil, errors.Wrap(err, "invalid config for key [token.selector]: expected retryInterval (duration) and numRetries (integer))")
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

// RateLimitEnabled reports whether the built-in default rate limiter should be
// wired in. The limiter is on by default; only an explicit negative RateLimit
// disables it.
func (c *Config) RateLimitEnabled() bool {
	return c.RateLimit >= 0
}

// GetRateLimit returns the per-wallet selection rate (requests/second) for the
// built-in limiter. An unset value (0) resolves to the default; a negative value
// is returned as-is and means the limiter is disabled (see RateLimitEnabled).
func (c *Config) GetRateLimit() float64 {
	if c.RateLimit == 0 {
		return defaultRateLimit
	}

	return c.RateLimit
}

// GetRateLimitBurst returns the per-wallet burst capacity for the built-in limiter.
func (c *Config) GetRateLimitBurst() float64 {
	if c.RateLimitBurst > 0 {
		return c.RateLimitBurst
	}

	return defaultRateLimitBurst
}

// GetRateLimitIdleTTL returns how long an idle per-wallet bucket is kept before eviction.
func (c *Config) GetRateLimitIdleTTL() time.Duration {
	if c.RateLimitIdleTTL > 0 {
		return c.RateLimitIdleTTL
	}

	return defaultRateLimitIdleTTL
}

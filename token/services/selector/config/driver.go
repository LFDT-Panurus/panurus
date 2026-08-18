/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"math"
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
	// defaultRateLimit is the per-wallet selection rate, in requests per second, applied when
	// rate limiting is enabled without an explicit rateLimit.
	defaultRateLimit = ratelimit.DefaultRate
	// defaultRateLimitBurstFactor multiplies the rate to obtain the burst capacity applied
	// when rate limiting is enabled without an explicit rateLimitBurst.
	defaultRateLimitBurstFactor = ratelimit.DefaultBurstFactor
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
	// RateLimitEnabled turns on the built-in per-wallet selection rate limiter with the
	// default rate and burst. Rate limiting is off unless this is set or RateLimit is
	// positive.
	RateLimitEnabled bool `yaml:"rateLimitEnabled,omitempty"`
	// RateLimit is the maximum number of selection requests per second a single wallet may
	// issue. A positive value implies RateLimitEnabled. A value <= 0 falls back to the
	// default rate when rate limiting is enabled some other way, and means "no limit"
	// otherwise.
	RateLimit float64 `yaml:"rateLimit,omitempty"`
	// RateLimitBurst is the maximum number of selection requests a single wallet may issue
	// back-to-back. If <= 0, it defaults to defaultRateLimitBurstFactor times the rate.
	RateLimitBurst int `yaml:"rateLimitBurst,omitempty"`
	// RateLimitMaxBuckets caps the number of wallet buckets the limiter keeps in memory. If
	// <= 0, the limiter's own default is used.
	RateLimitMaxBuckets int `yaml:"rateLimitMaxBuckets,omitempty"`
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

// IsRateLimitEnabled tells whether the built-in per-wallet selection rate limiter must be
// activated. It is off by default: either rateLimitEnabled is set explicitly, or a positive
// rateLimit is configured, which implies it.
func (c *Config) IsRateLimitEnabled() bool {
	return c.RateLimitEnabled || c.RateLimit > 0
}

// GetRateLimit returns the maximum number of selection requests per second allowed to a single
// wallet, or 0 when rate limiting is disabled. When enabled without an explicit positive
// rateLimit, it returns defaultRateLimit.
func (c *Config) GetRateLimit() float64 {
	if !c.IsRateLimitEnabled() {
		return 0
	}
	if c.RateLimit > 0 {
		return c.RateLimit
	}

	return defaultRateLimit
}

// GetRateLimitBurst returns the burst capacity of a wallet bucket, or 0 when rate limiting is
// disabled. When enabled without an explicit positive rateLimitBurst, it returns
// defaultRateLimitBurstFactor times the rate, rounded up, and never less than one request.
func (c *Config) GetRateLimitBurst() int {
	if !c.IsRateLimitEnabled() {
		return 0
	}
	if c.RateLimitBurst > 0 {
		return c.RateLimitBurst
	}

	return max(int(math.Ceil(c.GetRateLimit()*defaultRateLimitBurstFactor)), 1)
}

// GetRateLimitMaxBuckets returns the maximum number of wallet buckets the limiter may keep in
// memory. It returns 0 when not configured, which selects the limiter's own default.
func (c *Config) GetRateLimitMaxBuckets() int {
	return max(c.RateLimitMaxBuckets, 0)
}

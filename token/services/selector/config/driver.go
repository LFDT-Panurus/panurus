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

	// Security limits to prevent algorithmic attacks
	defaultMaxTokensPerSelection  = 10000            // Max tokens to iterate per selection
	defaultMaxLockAttempts        = 50000            // Max lock attempts per selection (5x iteration limit)
	defaultMaxRetries             = 10               // Max outer retry loops
	defaultMaxLocksPerTransaction = 5000             // Max concurrent locks held per transaction
	defaultSelectionTimeout       = 30 * time.Second // Wall-clock timeout for selection
)

//go:generate counterfeiter -o mock/config_service.go -fake-name ConfigService . configService
type configService interface {
	UnmarshalKey(key string, rawVal any) error
}

// Limits defines resource limits for token selection to prevent algorithmic attacks
type Limits struct {
	MaxTokensPerSelection  int           `yaml:"maxTokensPerSelection,omitempty"`
	MaxLockAttempts        int           `yaml:"maxLockAttempts,omitempty"`
	MaxRetries             int           `yaml:"maxRetries,omitempty"`
	MaxLocksPerTransaction int           `yaml:"maxLocksPerTransaction,omitempty"`
	SelectionTimeout       time.Duration `yaml:"selectionTimeout,omitempty"`
}

type Config struct {
	Driver                 driver.Driver `yaml:"driver,omitempty"`
	RetryInterval          time.Duration `yaml:"retryInterval,omitempty"`
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

	Limits Limits `yaml:"limits,omitempty"`

	// Deprecated: Use Limits.MaxRetries instead
	NumRetries int `yaml:"numRetries,omitempty"`
}

// New returns a SelectorConfig with the values from the token.selector key.
//
// On error the returned *Config is a usable zero value rather than nil, so the
// callers that log the error and carry on with defaults do not dereference nil.
// A partially unmarshalled struct is discarded: half-applied values are worse
// than defaults.
func New(config configService) (*Config, error) {
	c := &Config{}
	if err := config.UnmarshalKey("token.selector", c); err != nil {
		return &Config{}, errors.Wrap(err, "invalid config for key [token.selector]: expected retryInterval (duration) and numRetries (integer))")
	}

	return c, nil
}

func (c *Config) GetDriver() driver.Driver {
	if c.Driver == "" {
		return defaultDriver
	}

	return c.Driver
}

// GetNumRetries returns the number of retries (deprecated, use GetLimits().MaxRetries)
// Deprecated: Use GetLimits().MaxRetries instead
func (c *Config) GetNumRetries() int {
	// For backward compatibility, return NumRetries if set, otherwise use limits
	if c.NumRetries > 0 {
		return c.NumRetries
	}

	return c.GetLimits().MaxRetries
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

// GetLimits returns the resource limits configuration with defaults applied
func (c *Config) GetLimits() Limits {
	limits := c.Limits

	if limits.MaxTokensPerSelection <= 0 {
		limits.MaxTokensPerSelection = defaultMaxTokensPerSelection
	}
	if limits.MaxLockAttempts <= 0 {
		limits.MaxLockAttempts = defaultMaxLockAttempts
	}

	// Handle MaxRetries with backward compatibility
	if limits.MaxRetries <= 0 {
		if c.NumRetries > 0 {
			limits.MaxRetries = c.NumRetries
		} else {
			limits.MaxRetries = defaultMaxRetries
		}
	}

	if limits.MaxLocksPerTransaction <= 0 {
		limits.MaxLocksPerTransaction = defaultMaxLocksPerTransaction
	}
	if limits.SelectionTimeout <= 0 {
		limits.SelectionTimeout = defaultSelectionTimeout

		// Keep the default wall-clock timeout from binding before the retry
		// budget it is meant to bound. Both selectors sleep for up to
		// retryInterval between cycles, so a contended selection can spend
		// maxRetries * retryInterval backing off before it has used up its
		// retries. With the defaults (10 retries, 5s interval) that averages
		// ~25s and reaches 50s, so a fixed 30s ceiling would turn ordinary
		// contention into SelectorTimedOut — which callers cannot resolve by
		// retrying — instead of letting the retry budget play out.
		if budget := time.Duration(limits.MaxRetries)*c.GetRetryInterval() + defaultSelectionTimeout; budget > limits.SelectionTimeout {
			limits.SelectionTimeout = budget
		}
	}

	return limits
}

// GetMaxTokensPerSelection returns the maximum number of tokens to iterate per selection
func (c *Config) GetMaxTokensPerSelection() int {
	return c.GetLimits().MaxTokensPerSelection
}

// GetMaxLockAttempts returns the maximum number of lock attempts per selection
func (c *Config) GetMaxLockAttempts() int {
	return c.GetLimits().MaxLockAttempts
}

// GetMaxLocksPerTransaction returns the maximum number of concurrent locks per transaction
func (c *Config) GetMaxLocksPerTransaction() int {
	return c.GetLimits().MaxLocksPerTransaction
}

// GetSelectionTimeout returns the wall-clock timeout for selection operations
func (c *Config) GetSelectionTimeout() time.Duration {
	return c.GetLimits().SelectionTimeout
}

// Validate checks that the configuration is valid.
// Zero values are allowed (they mean "use default"); only relational invariants
// between explicit non-zero fields are checked.
func (c *Config) Validate() error {
	// Resolve effective values so relational checks use the same numbers that
	// the runtime will actually apply.
	limits := c.GetLimits()

	if c.Limits.MaxLockAttempts > 0 && c.Limits.MaxTokensPerSelection > 0 &&
		c.Limits.MaxLockAttempts < c.Limits.MaxTokensPerSelection {
		return errors.Errorf("maxLockAttempts (%d) should be >= maxTokensPerSelection (%d)",
			limits.MaxLockAttempts, limits.MaxTokensPerSelection)
	}
	if c.Limits.MaxLocksPerTransaction > 0 && c.Limits.MaxTokensPerSelection > 0 &&
		c.Limits.MaxLocksPerTransaction > c.Limits.MaxTokensPerSelection {
		return errors.Errorf("maxLocksPerTransaction (%d) should be <= maxTokensPerSelection (%d)",
			limits.MaxLocksPerTransaction, limits.MaxTokensPerSelection)
	}

	return nil
}

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

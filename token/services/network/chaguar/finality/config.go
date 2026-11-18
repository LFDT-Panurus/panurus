/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import "time"

const (
	// PollInterval is the configuration key for how often the shared poller sweeps the pending set
	PollInterval = "token.finality.poller.interval"
	// PollBatchSize is the configuration key for how many txIDs go into one committer status query
	PollBatchSize = "token.finality.poller.batchSize"
	// PendingTTL is the configuration key for how long a tx stays pending before its slot is
	// reclaimed; it should exceed the longest caller finality timeout
	PendingTTL = "token.finality.poller.pendingTTL"

	// DefaultPollInterval is the default sweep interval
	DefaultPollInterval = 1 * time.Second
	// DefaultPollBatchSize is the default status query batch size
	DefaultPollBatchSize = 2000
	// DefaultPendingTTL is the default pending slot TTL
	DefaultPendingTTL = 10 * time.Minute
)

// ConfigGetter models the configuration getter for the finality poller
type ConfigGetter interface {
	// PollInterval returns how often the shared poller sweeps the pending set
	PollInterval() time.Duration
	// PollBatchSize returns how many txIDs go into one committer status query
	PollBatchSize() int
	// PendingTTL returns how long a tx stays pending before its slot is reclaimed
	PendingTTL() time.Duration
}

// Configuration models the configuration for the finality poller.
//
//go:generate counterfeiter -o mock/configuration.go -fake-name Configuration . Configuration
type Configuration interface {
	// GetDuration returns the duration for the given key.
	GetDuration(key string) time.Duration
	// GetInt returns the int for the given key.
	GetInt(key string) int
}

// NewConfig creates a new ConfigGetter that uses the provided Configuration
// interface to retrieve finality poller settings.
func NewConfig(configuration Configuration) *serviceConfig {
	return &serviceConfig{configuration: configuration}
}

type serviceConfig struct {
	configuration Configuration
}

// PollInterval returns the sweep interval from the configuration.
// If the configured value is not greater than 0, it returns DefaultPollInterval.
func (c *serviceConfig) PollInterval() time.Duration {
	if v := c.configuration.GetDuration(PollInterval); v > 0 {
		return v
	}

	return DefaultPollInterval
}

// PollBatchSize returns the status query batch size from the configuration.
// If the configured value is not greater than 0, it returns DefaultPollBatchSize.
func (c *serviceConfig) PollBatchSize() int {
	if v := c.configuration.GetInt(PollBatchSize); v > 0 {
		return v
	}

	return DefaultPollBatchSize
}

// PendingTTL returns the pending slot TTL from the configuration.
// If the configured value is not greater than 0, it returns DefaultPendingTTL.
func (c *serviceConfig) PendingTTL() time.Duration {
	if v := c.configuration.GetDuration(PendingTTL); v > 0 {
		return v
	}

	return DefaultPendingTTL
}

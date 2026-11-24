/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package queue

const (
	// Workers is the configuration key for the number of worker goroutines
	Workers = "token.finality.notification.workers"
	// QueueSize is the configuration key for the size of the event buffer
	QueueSize = "token.finality.notification.queueSize"
	// DefaultWorkers is the default number of worker goroutines
	DefaultWorkers = 10
	// DefaultQueueSize is the default size of the event buffer
	DefaultQueueSize = 1000
)

// ConfigGetter models the configuration getter for the event queue
type ConfigGetter interface {
	// Workers returns the number of worker goroutines
	Workers() int
	// QueueSize returns the size of the event buffer
	QueueSize() int
}

//go:generate counterfeiter -o mock/configuration.go -fake-name Configuration . Configuration
type Configuration interface {
	GetInt(key string) int
}

// NewConfig creates a new ConfigGetter that uses the provided Configuration
// interface to retrieve event queue settings.
func NewConfig(configuration Configuration) *serviceConfig {
	return &serviceConfig{configuration: configuration}
}

type serviceConfig struct {
	configuration Configuration
}

// Workers returns the number of worker goroutines from the configuration.
// If the configured value is not greater than 0, it returns DefaultWorkers (10).
func (c *serviceConfig) Workers() int {
	if v := c.configuration.GetInt(Workers); v > 0 {
		return v
	}

	return DefaultWorkers
}

// QueueSize returns the size of the event buffer from the configuration.
// If the configured value is not greater than 0, it returns DefaultQueueSize (1000).
func (c *serviceConfig) QueueSize() int {
	if v := c.configuration.GetInt(QueueSize); v > 0 {
		return v
	}

	return DefaultQueueSize
}

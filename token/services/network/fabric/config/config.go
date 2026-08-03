/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"fmt"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

type ListenerManagerConfig interface {
	DeliveryMapperParallelism() int
	DeliveryBlockProcessParallelism() int
	DeliveryListenerTimeout() time.Duration
	DeliveryLRUSize() int
	DeliveryLRUBuffer() int
	DeliveryLedgerInfoAttempts() int
	DeliveryLedgerInfoRetryDelay() time.Duration
}

const (
	DeliveryMapperParallelism       = "token.finality.delivery.mapperParallelism"
	DeliveryBlockProcessParallelism = "token.finality.delivery.blockProcessParallelism"
	DeliveryLRUSize                 = "token.finality.delivery.lruSize"
	DeliveryLRUBuffer               = "token.finality.delivery.lruBuffer"
	DeliveryListenerTimeout         = "token.finality.delivery.listenerTimeout"
	// DeliveryLedgerInfoAttempts bounds how many times the current ledger height is
	// read before the block scan is refused. See finality.Delivery.
	DeliveryLedgerInfoAttempts = "token.finality.delivery.ledgerInfoAttempts"
	// DeliveryLedgerInfoRetryDelay is the pause before the first retry of that read;
	// it doubles on each further attempt, up to a ceiling that keeps a large
	// attempt budget from growing the pause without bound. See finality.Delivery.
	DeliveryLedgerInfoRetryDelay           = "token.finality.delivery.ledgerInfoRetryDelay"
	DefaultDeliveryMapperParallelism       = 10
	DefaultDeliveryBlockProcessParallelism = 10
	DefaultDeliveryLRUSize                 = 30
	DefaultDeliveryLRUBuffer               = 15
	DefaultDeliveryListenerTimeout         = 10 * time.Second
	// DefaultDeliveryLedgerInfoAttempts and DefaultDeliveryLedgerInfoRetryDelay
	// span ~31.5s of retries (0.5s + 1s + 2s + 4s + 8s + 16s). The budget has to
	// outlast a peer restart rather than a dropped packet: nothing retries the
	// block scan, so a height that stays unreadable costs the channel its
	// block-based finality until the process restarts.
	DefaultDeliveryLedgerInfoAttempts   = 7
	DefaultDeliveryLedgerInfoRetryDelay = 500 * time.Millisecond
)

type ManagerType string

const (
	Delivery ManagerType = "delivery"
)

func NewListenerManagerConfig(configService driver.ConfigService) *serviceListenerManagerConfig {
	return &serviceListenerManagerConfig{c: configService}
}

type serviceListenerManagerConfig struct {
	c driver.ConfigService
}

func (c *serviceListenerManagerConfig) DeliveryMapperParallelism() int {
	if v := c.c.GetInt(DeliveryMapperParallelism); v > 0 {
		return v
	}

	return DefaultDeliveryMapperParallelism
}

func (c *serviceListenerManagerConfig) DeliveryBlockProcessParallelism() int {
	if v := c.c.GetInt(DeliveryBlockProcessParallelism); v >= 0 {
		return v
	}

	return DefaultDeliveryBlockProcessParallelism
}

func (c *serviceListenerManagerConfig) DeliveryLRUSize() int {
	if v := c.c.GetInt(DeliveryLRUSize); v >= 0 {
		return v
	}

	return DefaultDeliveryLRUSize
}

func (c *serviceListenerManagerConfig) DeliveryLRUBuffer() int {
	if v := c.c.GetInt(DeliveryLRUBuffer); v >= 0 {
		return v
	}

	return DefaultDeliveryLRUBuffer
}

func (c *serviceListenerManagerConfig) DeliveryListenerTimeout() time.Duration {
	if v := c.c.GetDuration(DeliveryListenerTimeout); v >= 0 {
		return v
	}

	return DefaultDeliveryListenerTimeout
}

func (c *serviceListenerManagerConfig) DeliveryLedgerInfoAttempts() int {
	if v := c.c.GetInt(DeliveryLedgerInfoAttempts); v > 0 {
		return v
	}

	return DefaultDeliveryLedgerInfoAttempts
}

func (c *serviceListenerManagerConfig) DeliveryLedgerInfoRetryDelay() time.Duration {
	if v := c.c.GetDuration(DeliveryLedgerInfoRetryDelay); v > 0 {
		return v
	}

	return DefaultDeliveryLedgerInfoRetryDelay
}

func (c *serviceListenerManagerConfig) String() string {
	return fmt.Sprintf(
		"Delivery [mapperParalellism: %d, lru: (%d, %d), listenerTimeout: %v, ledgerInfo: (%d, %v)]",
		c.DeliveryMapperParallelism(),
		c.DeliveryLRUSize(),
		c.DeliveryLRUBuffer(),
		c.DeliveryListenerTimeout(),
		c.DeliveryLedgerInfoAttempts(),
		c.DeliveryLedgerInfoRetryDelay(),
	)
}

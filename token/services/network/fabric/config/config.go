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

// positiveOrDefault returns v when it is strictly positive, otherwise def. Use it
// for knobs with no "disabled" state, where 0 is meaningless and indistinguishable
// from an unset key: a non-positive or unset value is always the documented default.
func positiveOrDefault[T int | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}

	return def
}

// configuredOrDefault preserves an explicit, non-negative value the operator wrote
// — including 0, which the delivery layer reads as "remove this bound" (an unbounded
// cache, or a listener that never times out). isSet distinguishes a deliberate 0
// from an absent key, which GetInt/GetDuration also report as 0: only an unset key
// or a negative typo fall back to def. Contrast positiveOrDefault, which also
// replaces 0 and so cannot express that disabled mode.
func configuredOrDefault[T int | time.Duration](isSet bool, v, def T) T {
	if isSet && v >= 0 {
		return v // explicit 0 -> disabled mode, positive -> honoured as-is
	}

	return def // unset key or negative typo -> documented default
}

func (c *serviceListenerManagerConfig) DeliveryMapperParallelism() int {
	return positiveOrDefault(c.c.GetInt(DeliveryMapperParallelism), DefaultDeliveryMapperParallelism)
}

func (c *serviceListenerManagerConfig) DeliveryBlockProcessParallelism() int {
	return positiveOrDefault(c.c.GetInt(DeliveryBlockProcessParallelism), DefaultDeliveryBlockProcessParallelism)
}

func (c *serviceListenerManagerConfig) DeliveryLRUSize() int {
	return configuredOrDefault(c.c.IsSet(DeliveryLRUSize), c.c.GetInt(DeliveryLRUSize), DefaultDeliveryLRUSize)
}

func (c *serviceListenerManagerConfig) DeliveryLRUBuffer() int {
	return configuredOrDefault(c.c.IsSet(DeliveryLRUBuffer), c.c.GetInt(DeliveryLRUBuffer), DefaultDeliveryLRUBuffer)
}

func (c *serviceListenerManagerConfig) DeliveryListenerTimeout() time.Duration {
	return configuredOrDefault(c.c.IsSet(DeliveryListenerTimeout), c.c.GetDuration(DeliveryListenerTimeout), DefaultDeliveryListenerTimeout)
}

func (c *serviceListenerManagerConfig) DeliveryLedgerInfoAttempts() int {
	return positiveOrDefault(c.c.GetInt(DeliveryLedgerInfoAttempts), DefaultDeliveryLedgerInfoAttempts)
}

func (c *serviceListenerManagerConfig) DeliveryLedgerInfoRetryDelay() time.Duration {
	return positiveOrDefault(c.c.GetDuration(DeliveryLedgerInfoRetryDelay), DefaultDeliveryLedgerInfoRetryDelay)
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

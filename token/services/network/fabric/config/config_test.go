/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/stretchr/testify/assert"
)

// mapConfigService is a driver.ConfigService backed by two maps: whatever the test
// puts in them is "set", everything else reads as a zero value, which is what an
// absent YAML key looks like.
type mapConfigService struct {
	ints      map[string]int
	durations map[string]time.Duration
}

func (c *mapConfigService) GetInt(key string) int                { return c.ints[key] }
func (c *mapConfigService) GetDuration(key string) time.Duration { return c.durations[key] }
func (c *mapConfigService) GetString(string) string              { return "" }
func (c *mapConfigService) GetBool(string) bool                  { return false }
func (c *mapConfigService) GetStringSlice(string) []string       { return nil }
func (c *mapConfigService) IsSet(string) bool                    { return false }
func (c *mapConfigService) UnmarshalKey(string, any) error       { return nil }
func (c *mapConfigService) ConfigFileUsed() string               { return "" }
func (c *mapConfigService) GetPath(string) string                { return "" }
func (c *mapConfigService) TranslatePath(path string) string     { return path }

// TestLedgerInfoRetryDefaults pins the defaults an unconfigured deployment gets.
// The attempt budget matters beyond taste: nothing retries the block scan, so a
// height that stays unreadable costs the channel its block-based finality until
// the process restarts, and the budget has to outlast a peer restart.
func TestLedgerInfoRetryDefaults(t *testing.T) {
	c := config.NewListenerManagerConfig(&mapConfigService{})

	assert.Equal(t, config.DefaultDeliveryLedgerInfoAttempts, c.DeliveryLedgerInfoAttempts())
	assert.Equal(t, config.DefaultDeliveryLedgerInfoRetryDelay, c.DeliveryLedgerInfoRetryDelay())
	assert.GreaterOrEqual(t, totalRetryWait(c.DeliveryLedgerInfoAttempts(), c.DeliveryLedgerInfoRetryDelay()), 20*time.Second,
		"the default budget must outlast a peer restart, not just a dropped packet")
}

// TestLedgerInfoRetryReadsConfiguredValues covers the point of the exercise: an
// operator can shorten or lengthen the budget without a rebuild.
func TestLedgerInfoRetryReadsConfiguredValues(t *testing.T) {
	c := config.NewListenerManagerConfig(&mapConfigService{
		ints:      map[string]int{config.DeliveryLedgerInfoAttempts: 12},
		durations: map[string]time.Duration{config.DeliveryLedgerInfoRetryDelay: 250 * time.Millisecond},
	})

	assert.Equal(t, 12, c.DeliveryLedgerInfoAttempts())
	assert.Equal(t, 250*time.Millisecond, c.DeliveryLedgerInfoRetryDelay())
}

// TestLedgerInfoRetryRejectsNonPositiveValues covers the values a hand-edited YAML
// can hold: 0 attempts would refuse every scan and a negative delay would busy
// loop, so both fall back to the default rather than being honoured.
func TestLedgerInfoRetryRejectsNonPositiveValues(t *testing.T) {
	for _, attempts := range []int{0, -1} {
		c := config.NewListenerManagerConfig(&mapConfigService{ints: map[string]int{config.DeliveryLedgerInfoAttempts: attempts}})
		assert.Equal(t, config.DefaultDeliveryLedgerInfoAttempts, c.DeliveryLedgerInfoAttempts(), "attempts %d must not be honoured", attempts)
	}

	for _, delay := range []time.Duration{0, -time.Second} {
		c := config.NewListenerManagerConfig(&mapConfigService{durations: map[string]time.Duration{config.DeliveryLedgerInfoRetryDelay: delay}})
		assert.Equal(t, config.DefaultDeliveryLedgerInfoRetryDelay, c.DeliveryLedgerInfoRetryDelay(), "delay %v must not be honoured", delay)
	}
}

// TestStringReportsLedgerInfoBudget keeps the startup log line informative: the
// budget is otherwise invisible to an operator diagnosing a missing finality.
func TestStringReportsLedgerInfoBudget(t *testing.T) {
	s := config.NewListenerManagerConfig(&mapConfigService{}).String()

	assert.Contains(t, s, "ledgerInfo:")
}

// totalRetryWait sums the doubling schedule ledgerHeight sleeps through: one wait
// less than the number of attempts, each twice the previous.
func totalRetryWait(attempts int, first time.Duration) time.Duration {
	var total time.Duration
	for i, delay := 0, first; i < attempts-1; i, delay = i+1, delay*2 {
		total += delay
	}

	return total
}

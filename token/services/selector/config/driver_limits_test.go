/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_GetLimits(t *testing.T) {
	t.Run("returns defaults when limits not configured", func(t *testing.T) {
		cfg := &Config{}
		limits := cfg.GetLimits()

		assert.Equal(t, defaultMaxTokensPerSelection, limits.MaxTokensPerSelection)
		assert.Equal(t, defaultMaxLockAttempts, limits.MaxLockAttempts)
		assert.Equal(t, defaultMaxRetries, limits.MaxRetries)
		assert.Equal(t, defaultMaxLocksPerTransaction, limits.MaxLocksPerTransaction)
		// The default timeout is widened to clear the default retry budget
		// (maxRetries backoffs of up to retryInterval each), so that ordinary
		// contention plays out through the retries instead of tripping
		// SelectorTimedOut.
		assert.Equal(t, defaultTimeoutFor(defaultMaxRetries, defaultRetryInterval), limits.SelectionTimeout)
		assert.Greater(t, limits.SelectionTimeout, time.Duration(defaultMaxRetries)*defaultRetryInterval)
	})

	t.Run("returns configured values when set", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  5000,
				MaxLockAttempts:        25000,
				MaxRetries:             5,
				MaxLocksPerTransaction: 2500,
				SelectionTimeout:       15 * time.Second,
			},
		}
		limits := cfg.GetLimits()

		assert.Equal(t, 5000, limits.MaxTokensPerSelection)
		assert.Equal(t, 25000, limits.MaxLockAttempts)
		assert.Equal(t, 5, limits.MaxRetries)
		assert.Equal(t, 2500, limits.MaxLocksPerTransaction)
		assert.Equal(t, 15*time.Second, limits.SelectionTimeout)
	})

	t.Run("returns defaults for zero values", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  0,
				MaxLockAttempts:        0,
				MaxRetries:             0,
				MaxLocksPerTransaction: 0,
				SelectionTimeout:       0,
			},
		}
		limits := cfg.GetLimits()

		assert.Equal(t, defaultMaxTokensPerSelection, limits.MaxTokensPerSelection)
		assert.Equal(t, defaultMaxLockAttempts, limits.MaxLockAttempts)
		assert.Equal(t, defaultMaxRetries, limits.MaxRetries)
		assert.Equal(t, defaultMaxLocksPerTransaction, limits.MaxLocksPerTransaction)
		assert.Equal(t, defaultTimeoutFor(defaultMaxRetries, defaultRetryInterval), limits.SelectionTimeout)
	})

	t.Run("backward compatibility: uses deprecated NumRetries", func(t *testing.T) {
		cfg := &Config{
			NumRetries: 8, // Deprecated field
		}
		limits := cfg.GetLimits()

		assert.Equal(t, 8, limits.MaxRetries, "should use deprecated NumRetries value")
	})

	t.Run("backward compatibility: MaxRetries takes precedence over NumRetries", func(t *testing.T) {
		cfg := &Config{
			NumRetries: 8,
			Limits: Limits{
				MaxRetries: 5,
			},
		}
		limits := cfg.GetLimits()

		assert.Equal(t, 5, limits.MaxRetries, "MaxRetries should take precedence over deprecated NumRetries")
	})
}

func TestConfig_Validate(t *testing.T) {
	t.Run("valid configuration with defaults", func(t *testing.T) {
		cfg := &Config{}
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("valid configuration with custom values", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  5000,
				MaxLockAttempts:        25000,
				MaxRetries:             5,
				MaxLocksPerTransaction: 2500,
				SelectionTimeout:       15 * time.Second,
			},
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("invalid: maxLockAttempts less than maxTokensPerSelection", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  10000,
				MaxLockAttempts:        5000, // Less than MaxTokensPerSelection
				MaxRetries:             10,
				MaxLocksPerTransaction: 5000,
				SelectionTimeout:       30 * time.Second,
			},
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maxLockAttempts")
		assert.Contains(t, err.Error(), "maxTokensPerSelection")
	})

	t.Run("invalid: maxLocksPerTransaction greater than maxTokensPerSelection", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  5000,
				MaxLockAttempts:        25000,
				MaxRetries:             10,
				MaxLocksPerTransaction: 10000, // Greater than MaxTokensPerSelection
				SelectionTimeout:       30 * time.Second,
			},
		}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maxLocksPerTransaction")
		assert.Contains(t, err.Error(), "maxTokensPerSelection")
	})

	t.Run("edge case: all limits equal", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  5000,
				MaxLockAttempts:        5000,
				MaxRetries:             5,
				MaxLocksPerTransaction: 5000,
				SelectionTimeout:       30 * time.Second,
			},
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})

	t.Run("edge case: minimal valid values", func(t *testing.T) {
		cfg := &Config{
			Limits: Limits{
				MaxTokensPerSelection:  1,
				MaxLockAttempts:        1,
				MaxRetries:             1,
				MaxLocksPerTransaction: 1,
				SelectionTimeout:       1 * time.Nanosecond,
			},
		}
		err := cfg.Validate()
		require.NoError(t, err)
	})
}

func TestConfig_DefaultValues(t *testing.T) {
	t.Run("default values are reasonable", func(t *testing.T) {
		// Verify defaults are set to secure values
		assert.Equal(t, 10000, defaultMaxTokensPerSelection, "default max tokens should be 10k")
		assert.Equal(t, 50000, defaultMaxLockAttempts, "default max lock attempts should be 50k")
		assert.Equal(t, 10, defaultMaxRetries, "default max retries should be 10")
		assert.Equal(t, 5000, defaultMaxLocksPerTransaction, "default max locks per tx should be 5k")
		assert.Equal(t, 30*time.Second, defaultSelectionTimeout, "default timeout should be 30s")
	})

	t.Run("default relationships are valid", func(t *testing.T) {
		// Verify default values satisfy validation rules
		assert.GreaterOrEqual(t, defaultMaxLockAttempts, defaultMaxTokensPerSelection,
			"maxLockAttempts should be >= maxTokensPerSelection")
		assert.LessOrEqual(t, defaultMaxLocksPerTransaction, defaultMaxTokensPerSelection,
			"maxLocksPerTransaction should be <= maxTokensPerSelection")
	})
}

// defaultTimeoutFor mirrors the derivation in GetLimits: the fixed default plus
// the worst-case time the selector may spend backing off between retries.
func defaultTimeoutFor(maxRetries int, retryInterval time.Duration) time.Duration {
	return time.Duration(maxRetries)*retryInterval + defaultSelectionTimeout
}

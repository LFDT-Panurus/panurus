/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/chaguar/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/network/chaguar/lookup/mock"
	"github.com/stretchr/testify/assert"
)

func TestNewConfig(t *testing.T) {
	m := &mock.Configuration{}
	cfg := lookup.NewConfig(m)
	assert.NotNil(t, cfg)
}

func TestServiceConfig_PermanentInterval(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		expected := 2 * time.Minute
		m.GetDurationReturns(expected)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, expected, cfg.PermanentInterval())
		assert.Equal(t, 1, m.GetDurationCallCount())
		assert.Equal(t, lookup.PermanentInterval, m.GetDurationArgsForCall(0))
	})

	t.Run("returns default when configuration returns 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(0)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, lookup.DefaultPermanentInterval, cfg.PermanentInterval())
	})

	t.Run("returns default when configuration returns negative", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(-1 * time.Second)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, lookup.DefaultPermanentInterval, cfg.PermanentInterval())
	})
}

func TestServiceConfig_OnceDeadline(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		expected := 10 * time.Minute
		m.GetDurationReturns(expected)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, expected, cfg.OnceDeadline())
		assert.Equal(t, 1, m.GetDurationCallCount())
		assert.Equal(t, lookup.OnceDeadline, m.GetDurationArgsForCall(0))
	})

	t.Run("returns default when configuration returns 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(0)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, lookup.DefaultOnceDeadline, cfg.OnceDeadline())
	})

	t.Run("returns default when configuration returns negative", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(-1 * time.Second)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, lookup.DefaultOnceDeadline, cfg.OnceDeadline())
	})
}

func TestServiceConfig_OnceInterval(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		expected := 5 * time.Second
		m.GetDurationReturns(expected)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, expected, cfg.OnceInterval())
		assert.Equal(t, 1, m.GetDurationCallCount())
		assert.Equal(t, lookup.OnceInterval, m.GetDurationArgsForCall(0))
	})

	t.Run("returns default when configuration returns 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(0)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, lookup.DefaultOnceInterval, cfg.OnceInterval())
	})

	t.Run("returns default when configuration returns negative", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(-1 * time.Second)
		cfg := lookup.NewConfig(m)

		assert.Equal(t, lookup.DefaultOnceInterval, cfg.OnceInterval())
	})
}

func TestServiceConfig_DeprecatedFabricxKeys(t *testing.T) {
	for _, tc := range []struct {
		name       string
		current    string
		deprecated string
		get        func(lookup.ConfigGetter) time.Duration
	}{
		{
			name:       "permanent.interval",
			current:    lookup.PermanentInterval,
			deprecated: "token.fabricx.lookup.permanent.interval",
			get:        func(c lookup.ConfigGetter) time.Duration { return c.PermanentInterval() },
		},
		{
			name:       "once.deadline",
			current:    lookup.OnceDeadline,
			deprecated: "token.fabricx.lookup.once.deadline",
			get:        func(c lookup.ConfigGetter) time.Duration { return c.OnceDeadline() },
		},
		{
			name:       "once.interval",
			current:    lookup.OnceInterval,
			deprecated: "token.fabricx.lookup.once.interval",
			get:        func(c lookup.ConfigGetter) time.Duration { return c.OnceInterval() },
		},
	} {
		t.Run(tc.name+": falls back to the deprecated key when unset", func(t *testing.T) {
			m := &mock.Configuration{}
			m.GetDurationStub = func(key string) time.Duration {
				if key == tc.deprecated {
					return 42 * time.Second
				}

				return 0
			}

			assert.Equal(t, 42*time.Second, tc.get(lookup.NewConfig(m)))
		})

		t.Run(tc.name+": prefers the current key over the deprecated one", func(t *testing.T) {
			m := &mock.Configuration{}
			m.GetDurationStub = func(key string) time.Duration {
				switch key {
				case tc.current:
					return 7 * time.Second
				case tc.deprecated:
					return 42 * time.Second
				}

				return 0
			}

			assert.Equal(t, 7*time.Second, tc.get(lookup.NewConfig(m)))
		})
	}
}

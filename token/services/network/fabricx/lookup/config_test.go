/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lookup_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/lookup"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/lookup/mock"
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

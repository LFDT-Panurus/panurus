/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package queue_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/queue"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/queue/mock"
	"github.com/stretchr/testify/assert"
)

func TestNewConfig(t *testing.T) {
	m := &mock.Configuration{}
	cfg := queue.NewConfig(m)
	assert.NotNil(t, cfg)
}

func TestServiceConfig_Workers(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(20)
		cfg := queue.NewConfig(m)

		assert.Equal(t, 20, cfg.Workers())
		assert.Equal(t, 1, m.GetIntCallCount())
		assert.Equal(t, queue.Workers, m.GetIntArgsForCall(0))
	})

	t.Run("returns default when configuration returns 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(0)
		cfg := queue.NewConfig(m)

		assert.Equal(t, queue.DefaultWorkers, cfg.Workers())
	})

	t.Run("returns default when configuration returns negative", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(-5)
		cfg := queue.NewConfig(m)

		assert.Equal(t, queue.DefaultWorkers, cfg.Workers())
	})
}

func TestServiceConfig_QueueSize(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(500)
		cfg := queue.NewConfig(m)

		assert.Equal(t, 500, cfg.QueueSize())
		assert.Equal(t, 1, m.GetIntCallCount())
		assert.Equal(t, queue.QueueSize, m.GetIntArgsForCall(0))
	})

	t.Run("returns default when configuration returns 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(0)
		cfg := queue.NewConfig(m)

		assert.Equal(t, queue.DefaultQueueSize, cfg.QueueSize())
	})

	t.Run("returns default when configuration returns negative", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(-10)
		cfg := queue.NewConfig(m)

		assert.Equal(t, queue.DefaultQueueSize, cfg.QueueSize())
	})
}

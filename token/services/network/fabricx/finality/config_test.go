/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/mock"
	"github.com/stretchr/testify/assert"
)

func TestServiceConfig_PollInterval(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(2 * time.Second)
		cfg := finality.NewConfig(m)

		assert.Equal(t, 2*time.Second, cfg.PollInterval())
		assert.Equal(t, finality.PollInterval, m.GetDurationArgsForCall(0))
	})

	t.Run("returns default when not set", func(t *testing.T) {
		m := &mock.Configuration{}
		cfg := finality.NewConfig(m)

		assert.Equal(t, finality.DefaultPollInterval, cfg.PollInterval())
	})
}

func TestServiceConfig_PollBatchSize(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetIntReturns(500)
		cfg := finality.NewConfig(m)

		assert.Equal(t, 500, cfg.PollBatchSize())
		assert.Equal(t, finality.PollBatchSize, m.GetIntArgsForCall(0))
	})

	t.Run("returns default when not set", func(t *testing.T) {
		m := &mock.Configuration{}
		cfg := finality.NewConfig(m)

		assert.Equal(t, finality.DefaultPollBatchSize, cfg.PollBatchSize())
	})
}

func TestServiceConfig_PendingTTL(t *testing.T) {
	t.Run("returns value from configuration when > 0", func(t *testing.T) {
		m := &mock.Configuration{}
		m.GetDurationReturns(time.Hour)
		cfg := finality.NewConfig(m)

		assert.Equal(t, time.Hour, cfg.PendingTTL())
		assert.Equal(t, finality.PendingTTL, m.GetDurationArgsForCall(0))
	})

	t.Run("returns default when not set", func(t *testing.T) {
		m := &mock.Configuration{}
		cfg := finality.NewConfig(m)

		assert.Equal(t, finality.DefaultPendingTTL, cfg.PendingTTL())
	})
}

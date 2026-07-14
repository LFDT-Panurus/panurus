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
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfig struct {
	permanentInterval time.Duration
	onceDeadline      time.Duration
	onceInterval      time.Duration
}

func (c *testConfig) PermanentInterval() time.Duration { return c.permanentInterval }
func (c *testConfig) OnceDeadline() time.Duration      { return c.onceDeadline }
func (c *testConfig) OnceInterval() time.Duration      { return c.onceInterval }

// TestCronListenerManager tests the registration and execution of lookup listeners using gocron.
func TestCronListenerManager(t *testing.T) {
	qs := &mock.QueryService{}
	config := &testConfig{
		permanentInterval: 100 * time.Millisecond,
		onceDeadline:      1 * time.Second,
		onceInterval:      50 * time.Millisecond,
	}

	mgr, err := lookup.NewCronListenerManager(qs, config)
	require.NoError(t, err)
	defer func() {
		_ = mgr.Stop()
	}()

	assert.True(t, mgr.PermanentLookupListenerSupported())

	t.Run("PermanentFound", func(t *testing.T) {
		// Given a permanent lookup listener, when a key is found, then the listener is notified.
		// When the value changes, then the listener is notified again.
		l := &mock.Listener{}
		qs.GetStateReturns(&driver2.VaultValue{Raw: []byte("v1")}, nil)

		err := mgr.AddPermanentLookupListener("ns", "key-perm", l)
		require.NoError(t, err)

		// Wait for the job to run at least once
		assert.Eventually(t, func() bool {
			return l.OnStatusCallCount() >= 1
		}, 1*time.Second, 50*time.Millisecond)

		// Change value
		qs.GetStateReturns(&driver2.VaultValue{Raw: []byte("v2")}, nil)
		assert.Eventually(t, func() bool {
			return l.OnStatusCallCount() >= 2
		}, 1*time.Second, 50*time.Millisecond)

		// Remove
		require.NoError(t, mgr.RemoveLookupListener("key-perm", l))
		currentCount := l.OnStatusCallCount()
		time.Sleep(200 * time.Millisecond)
		assert.Equal(t, currentCount, l.OnStatusCallCount())
	})

	t.Run("OnceFound", func(t *testing.T) {
		// Given a lookup listener, when a key is found, then the listener is notified and the job is stopped.
		l := &mock.Listener{}
		qs.GetStateReturns(&driver2.VaultValue{Raw: []byte("once")}, nil)

		err := mgr.AddLookupListener("ns", "key-once", l)
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			return l.OnStatusCallCount() == 1
		}, 1*time.Second, 50*time.Millisecond)

		// It should stop after finding it
		time.Sleep(200 * time.Millisecond)
		assert.Equal(t, 1, l.OnStatusCallCount())
	})

	t.Run("OnceDeadline", func(t *testing.T) {
		// Given a lookup listener, when a key is not found and the deadline is reached, then the listener is notified with an error and the job is stopped.
		l := &mock.Listener{}
		qs.GetStateReturns(nil, nil)

		config.onceDeadline = 100 * time.Millisecond
		config.onceInterval = 50 * time.Millisecond

		err := mgr.AddLookupListener("ns", "key-deadline", l)
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			return l.OnErrorCallCount() == 1
		}, 2*time.Second, 50*time.Millisecond)

		// It should stop after deadline
		time.Sleep(200 * time.Millisecond)
		assert.Equal(t, 1, l.OnErrorCallCount())
	})
}

// TestCronNSListenerManagerProvider tests the creation of a CronListenerManager.
func TestCronNSListenerManagerProvider(t *testing.T) {
	qs := &mock.QueryService{}
	qsp := &mock.QueryServiceProvider{}
	qsp.GetReturns(qs, nil)

	p := lookup.NewCronNSListenerManagerProvider(qsp, &testConfig{})

	mgr, err := p.NewManager("network", "channel")
	require.NoError(t, err)
	assert.NotNil(t, mgr)

	cronMgr, ok := mgr.(*lookup.CronListenerManager)
	assert.True(t, ok)
	require.NoError(t, cronMgr.Stop())
}

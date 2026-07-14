/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network

import (
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	selector "github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mock implementations for testing
type mockTTXStoreServiceManager struct {
	mock.Mock
}

func (m *mockTTXStoreServiceManager) StoreServiceByTMSId(tmsID token.TMSID) (*ttxdb.StoreService, error) {
	args := m.Called(tmsID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*ttxdb.StoreService), args.Error(1)
}

func TestNewLockerProvider(t *testing.T) {
	ttxMgr := &mockTTXStoreServiceManager{}
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	provider := NewLockerProvider(ttxMgr, sleepTimeout, validTxEvictionTimeout)

	require.NotNil(t, provider)
	assert.Equal(t, ttxMgr, provider.ttxStoreServiceManager)
	assert.Equal(t, sleepTimeout, provider.sleepTimeout)
	assert.Equal(t, validTxEvictionTimeout, provider.validTxEvictionTimeout)
}

func TestLockerProvider_New_Success(t *testing.T) {
	ttxMgr := &mockTTXStoreServiceManager{}
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	network := "test-network"
	channel := "test-channel"
	namespace := "test-namespace"

	expectedTMSID := token.TMSID{
		Network:   network,
		Channel:   channel,
		Namespace: namespace,
	}

	mockTTXStore := &ttxdb.StoreService{}
	ttxMgr.On("StoreServiceByTMSId", expectedTMSID).Return(mockTTXStore, nil)

	provider := NewLockerProvider(ttxMgr, sleepTimeout, validTxEvictionTimeout)
	locker, err := provider.New(network, channel, namespace)

	require.NoError(t, err)
	assert.NotNil(t, locker)
	ttxMgr.AssertExpectations(t)
}

func TestLockerProvider_New_Error(t *testing.T) {
	ttxMgr := &mockTTXStoreServiceManager{}
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	network := "test-network"
	channel := "test-channel"
	namespace := "test-namespace"

	expectedTMSID := token.TMSID{
		Network:   network,
		Channel:   channel,
		Namespace: namespace,
	}

	expectedErr := errors.New("store service error")
	ttxMgr.On("StoreServiceByTMSId", expectedTMSID).Return(nil, expectedErr)

	provider := NewLockerProvider(ttxMgr, sleepTimeout, validTxEvictionTimeout)
	locker, err := provider.New(network, channel, namespace)

	require.Error(t, err)
	assert.Nil(t, locker)
	assert.Equal(t, expectedErr, err)
	ttxMgr.AssertExpectations(t)
}

func TestLockerProvider_New_MultipleNetworks(t *testing.T) {
	ttxMgr := &mockTTXStoreServiceManager{}
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	tmsID1 := token.TMSID{
		Network:   "network1",
		Channel:   "channel1",
		Namespace: "namespace1",
	}

	tmsID2 := token.TMSID{
		Network:   "network2",
		Channel:   "channel2",
		Namespace: "namespace2",
	}

	mockTTXStore1 := &ttxdb.StoreService{}
	mockTTXStore2 := &ttxdb.StoreService{}

	ttxMgr.On("StoreServiceByTMSId", tmsID1).Return(mockTTXStore1, nil)
	ttxMgr.On("StoreServiceByTMSId", tmsID2).Return(mockTTXStore2, nil)

	provider := NewLockerProvider(ttxMgr, sleepTimeout, validTxEvictionTimeout)

	locker1, err := provider.New(tmsID1.Network, tmsID1.Channel, tmsID1.Namespace)
	require.NoError(t, err)
	assert.NotNil(t, locker1)

	locker2, err := provider.New(tmsID2.Network, tmsID2.Channel, tmsID2.Namespace)
	require.NoError(t, err)
	assert.NotNil(t, locker2)

	ttxMgr.AssertExpectations(t)
}

func TestLockerProvider_New_WithEmptyParameters(t *testing.T) {
	ttxMgr := &mockTTXStoreServiceManager{}
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	emptyTMSID := token.TMSID{
		Network:   "",
		Channel:   "",
		Namespace: "",
	}

	mockTTXStore := &ttxdb.StoreService{}
	ttxMgr.On("StoreServiceByTMSId", emptyTMSID).Return(mockTTXStore, nil)

	provider := NewLockerProvider(ttxMgr, sleepTimeout, validTxEvictionTimeout)
	locker, err := provider.New("", "", "")

	require.NoError(t, err)
	assert.NotNil(t, locker)
	ttxMgr.AssertExpectations(t)
}

func TestLockerProvider_New_WithDifferentTimeouts(t *testing.T) {
	testCases := []struct {
		name                   string
		sleepTimeout           time.Duration
		validTxEvictionTimeout time.Duration
	}{
		{
			name:                   "Short timeouts",
			sleepTimeout:           10 * time.Millisecond,
			validTxEvictionTimeout: 1 * time.Second,
		},
		{
			name:                   "Long timeouts",
			sleepTimeout:           1 * time.Second,
			validTxEvictionTimeout: 1 * time.Hour,
		},
		{
			name:                   "Zero timeouts",
			sleepTimeout:           0,
			validTxEvictionTimeout: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ttxMgr := &mockTTXStoreServiceManager{}
			provider := NewLockerProvider(ttxMgr, tc.sleepTimeout, tc.validTxEvictionTimeout)

			require.NotNil(t, provider)
			assert.Equal(t, tc.sleepTimeout, provider.sleepTimeout)
			assert.Equal(t, tc.validTxEvictionTimeout, provider.validTxEvictionTimeout)
		})
	}
}

func TestLockerProvider_InterfaceCompliance(t *testing.T) {
	// Verify that LockerProvider implements the expected interface
	var _ selector.LockerProvider = (*LockerProvider)(nil)
}

func TestLockerProvider_Concurrent(t *testing.T) {
	ttxMgr := &mockTTXStoreServiceManager{}
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	mockTTXStore := &ttxdb.StoreService{}
	// Allow multiple calls
	ttxMgr.On("StoreServiceByTMSId", tmsID).Return(mockTTXStore, nil)

	provider := NewLockerProvider(ttxMgr, sleepTimeout, validTxEvictionTimeout)

	// Test concurrent calls to New
	done := make(chan bool, 10)
	for range 10 {
		go func() {
			defer func() { done <- true }()
			locker, err := provider.New(tmsID.Network, tmsID.Channel, tmsID.Namespace)
			_ = locker
			_ = err
		}()
	}

	// Wait for all goroutines to complete
	for range 10 {
		<-done
	}
}

func TestLockerProvider_WithNilManager(t *testing.T) {
	sleepTimeout := 100 * time.Millisecond
	validTxEvictionTimeout := 5 * time.Minute

	// Test with nil manager - should still create provider
	provider := NewLockerProvider(nil, sleepTimeout, validTxEvictionTimeout)
	require.NotNil(t, provider)
	assert.Nil(t, provider.ttxStoreServiceManager)
}

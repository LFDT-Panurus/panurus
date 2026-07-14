/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup_test

import (
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled:      true,
		TTL:          24 * time.Hour,
		ScanInterval: 1 * time.Hour,
		BatchSize:    100,
		WorkerCount:  4,
	}

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	require.NotNil(t, manager)
}

func TestManager_StartStop(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled:      true,
		TTL:          100 * time.Millisecond,
		ScanInterval: 100 * time.Millisecond,
		BatchSize:    100,
		WorkerCount:  1,
	}

	mockLeadership := &mock.Leadership{}
	mockLeadership.CloseReturns(nil)
	mockStorage.AcquireCleanupLeadershipReturns(mockLeadership, true, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturns([]cleanup.DeletedToken{}, nil)

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err)

	// Starting again should return error
	err = manager.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")

	// Wait a bit to let it scan at least once
	time.Sleep(200 * time.Millisecond)

	// Stop the manager
	err = manager.Stop()
	require.NoError(t, err)

	assert.GreaterOrEqual(t, mockStorage.GetDeletedTokensPendingSKICleanupCallCount(), 1)
}

func TestManager_CleanupToken(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	mockKeystore := &mock.Keystore{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled:      true,
		TTL:          100 * time.Millisecond,
		ScanInterval: 100 * time.Millisecond,
		BatchSize:    100,
		WorkerCount:  1,
	}

	// Create a deleted token
	deletedToken := cleanup.DeletedToken{
		TxID:          "tx123",
		Index:         0,
		OwnerIdentity: []byte("owner-identity"),
		OwnerType:     "idemix",
		DeletedAt:     time.Now().Add(-2 * time.Hour),
	}

	mockLeadership1 := &mock.Leadership{}
	mockLeadership1.CloseReturns(nil)
	mockLeadership2 := &mock.Leadership{}
	mockLeadership2.CloseReturns(nil)

	mockStorage.AcquireCleanupLeadershipReturnsOnCall(0, mockLeadership1, true, nil)
	mockStorage.AcquireCleanupLeadershipReturns(mockLeadership2, true, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturnsOnCall(0, []cleanup.DeletedToken{deletedToken}, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturns([]cleanup.DeletedToken{}, nil)
	mockIdentityProvider.GetSKIsFromIdentityReturns([]string{"ski1", "ski2"}, nil)
	mockKeystoreProvider.KeystoreReturns(mockKeystore, nil)
	mockKeystore.DeleteReturns(nil)
	mockStorage.MarkTokenCleanedReturns(nil)

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err)

	// Wait for cleanup to happen
	time.Sleep(300 * time.Millisecond)

	// Stop the manager
	err = manager.Stop()
	require.NoError(t, err)

	// Verify the token was processed
	assert.GreaterOrEqual(t, mockIdentityProvider.GetSKIsFromIdentityCallCount(), 1)
	_, identity, identityType := mockIdentityProvider.GetSKIsFromIdentityArgsForCall(0)
	assert.Equal(t, []byte("owner-identity"), identity)
	assert.Equal(t, "idemix", identityType)

	// Verify keys were deleted
	assert.Equal(t, 2, mockKeystore.DeleteCallCount())
	ski1 := mockKeystore.DeleteArgsForCall(0)
	ski2 := mockKeystore.DeleteArgsForCall(1)
	assert.Contains(t, []string{"ski1", "ski2"}, ski1)
	assert.Contains(t, []string{"ski1", "ski2"}, ski2)

	// Verify token was marked as cleaned
	assert.GreaterOrEqual(t, mockStorage.MarkTokenCleanedCallCount(), 1)
	_, txID, index, cleanedBy := mockStorage.MarkTokenCleanedArgsForCall(0)
	assert.NotEmpty(t, cleanedBy)
	assert.Equal(t, "tx123", txID)
	assert.Equal(t, uint64(0), index)
}

func TestManager_CleanupToken_NoSKIs(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	mockKeystore := &mock.Keystore{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled:      true,
		TTL:          100 * time.Millisecond,
		ScanInterval: 100 * time.Millisecond,
		BatchSize:    100,
		WorkerCount:  1,
	}

	deletedToken := cleanup.DeletedToken{
		TxID:          "tx456",
		Index:         1,
		OwnerIdentity: []byte("owner-identity"),
		OwnerType:     "x509",
		DeletedAt:     time.Now().Add(-2 * time.Hour),
	}

	mockLeadership1 := &mock.Leadership{}
	mockLeadership1.CloseReturns(nil)
	mockLeadership2 := &mock.Leadership{}
	mockLeadership2.CloseReturns(nil)

	mockStorage.AcquireCleanupLeadershipReturnsOnCall(0, mockLeadership1, true, nil)
	mockStorage.AcquireCleanupLeadershipReturns(mockLeadership2, true, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturnsOnCall(0, []cleanup.DeletedToken{deletedToken}, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturns([]cleanup.DeletedToken{}, nil)
	mockIdentityProvider.GetSKIsFromIdentityReturns([]string{}, nil) // No SKIs
	mockKeystoreProvider.KeystoreReturns(mockKeystore, nil)
	mockStorage.MarkTokenCleanedReturns(nil)

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	err := manager.Start()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	err = manager.Stop()
	require.NoError(t, err)

	// Should still mark as cleaned even with no SKIs
	assert.GreaterOrEqual(t, mockStorage.MarkTokenCleanedCallCount(), 1)
	// Should not attempt to delete any keys
	assert.Equal(t, 0, mockKeystore.DeleteCallCount())
}

func TestManager_CleanupToken_KeyDeletionFailure(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	mockKeystore := &mock.Keystore{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled:      true,
		TTL:          100 * time.Millisecond,
		ScanInterval: 100 * time.Millisecond,
		BatchSize:    100,
		WorkerCount:  1,
	}

	deletedToken := cleanup.DeletedToken{
		TxID:          "tx789",
		Index:         2,
		OwnerIdentity: []byte("owner-identity"),
		OwnerType:     "idemix",
		DeletedAt:     time.Now().Add(-2 * time.Hour),
	}

	mockLeadership1 := &mock.Leadership{}
	mockLeadership1.CloseReturns(nil)
	mockLeadership2 := &mock.Leadership{}
	mockLeadership2.CloseReturns(nil)

	mockStorage.AcquireCleanupLeadershipReturnsOnCall(0, mockLeadership1, true, nil)
	mockStorage.AcquireCleanupLeadershipReturns(mockLeadership2, true, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturnsOnCall(0, []cleanup.DeletedToken{deletedToken}, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturns([]cleanup.DeletedToken{}, nil)
	mockIdentityProvider.GetSKIsFromIdentityReturns([]string{"ski1"}, nil)
	mockKeystoreProvider.KeystoreReturns(mockKeystore, nil)
	mockKeystore.DeleteReturns(errors.New("key not found"))
	mockStorage.MarkTokenCleanedReturns(nil)

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	err := manager.Start()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	err = manager.Stop()
	require.NoError(t, err)

	// Should attempt to delete the key
	assert.Equal(t, 1, mockKeystore.DeleteCallCount())
	// Should NOT mark as cleaned if all deletions failed
	assert.Equal(t, 0, mockStorage.MarkTokenCleanedCallCount())
}

func TestManager_CleanupToken_PartialFailure(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	mockKeystore := &mock.Keystore{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled:      true,
		TTL:          100 * time.Millisecond,
		ScanInterval: 100 * time.Millisecond,
		BatchSize:    100,
		WorkerCount:  1,
	}

	deletedToken := cleanup.DeletedToken{
		TxID:          "tx999",
		Index:         3,
		OwnerIdentity: []byte("owner-identity"),
		OwnerType:     "idemix",
		DeletedAt:     time.Now().Add(-2 * time.Hour),
	}

	mockLeadership1 := &mock.Leadership{}
	mockLeadership1.CloseReturns(nil)
	mockLeadership2 := &mock.Leadership{}
	mockLeadership2.CloseReturns(nil)

	mockStorage.AcquireCleanupLeadershipReturnsOnCall(0, mockLeadership1, true, nil)
	mockStorage.AcquireCleanupLeadershipReturns(mockLeadership2, true, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturnsOnCall(0, []cleanup.DeletedToken{deletedToken}, nil)
	mockStorage.GetDeletedTokensPendingSKICleanupReturns([]cleanup.DeletedToken{}, nil)
	mockIdentityProvider.GetSKIsFromIdentityReturns([]string{"ski1", "ski2"}, nil)
	mockKeystoreProvider.KeystoreReturns(mockKeystore, nil)
	// First deletion succeeds, second fails
	mockKeystore.DeleteReturnsOnCall(0, nil)
	mockKeystore.DeleteReturnsOnCall(1, errors.New("key not found"))
	mockStorage.MarkTokenCleanedReturns(nil)

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	err := manager.Start()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	err = manager.Stop()
	require.NoError(t, err)

	// Should attempt to delete both keys
	assert.Equal(t, 2, mockKeystore.DeleteCallCount())
	// Should not mark as cleaned with partial failure
	assert.GreaterOrEqual(t, mockStorage.MarkTokenCleanedCallCount(), 0)
}

func TestDefaultConfig(t *testing.T) {
	config := cleanup.DefaultConfig()

	assert.False(t, config.Enabled, "Cleanup should be disabled by default")
	assert.Equal(t, 24*time.Hour, config.TTL)
	assert.Equal(t, 1*time.Hour, config.ScanInterval)
	assert.Equal(t, 100, config.BatchSize)
	assert.Equal(t, 1, config.WorkerCount)
}

func TestManager_DisabledConfig(t *testing.T) {
	logger := logging.MustGetLogger()
	mockStorage := &mock.Storage{}
	mockIdentityProvider := &mock.IdentityProvider{}
	mockKeystoreProvider := &mock.KeystoreProvider{}
	tmsID := token.TMSID{Network: "test", Channel: "testchannel", Namespace: "testns"}
	config := cleanup.Config{
		Enabled: false,
	}

	manager := cleanup.NewManager(
		logger,
		mockStorage,
		mockIdentityProvider,
		mockKeystoreProvider,
		tmsID,
		config,
	)

	// Start should succeed but not actually start the loop
	err := manager.Start()
	require.NoError(t, err)

	// Stop should also succeed
	err = manager.Stop()
	require.NoError(t, err)

	// Storage should never be called
	assert.Equal(t, 0, mockStorage.GetDeletedTokensPendingSKICleanupCallCount())
}

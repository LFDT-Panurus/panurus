/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity_test

import (
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/sdk/identity"
	"github.com/LFDT-Panurus/panurus/token/sdk/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/identitydb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/keystoredb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/walletdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewDBStorageProvider verifies that NewDBStorageProvider correctly initializes
// a DBStorageProvider with the provided store service managers.
func TestNewDBStorageProvider(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)

	require.NotNil(t, provider)
}

// TestDBStorageProvider_WalletStore_Success verifies that WalletStore successfully
// retrieves a wallet store service for a given TMS ID.
func TestDBStorageProvider_WalletStore_Success(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	mockWalletStore := &walletdb.StoreService{}
	walletMgr.StoreServiceByTMSIdReturns(mockWalletStore, nil)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)
	store, err := provider.WalletStore(tmsID)

	require.NoError(t, err)
	assert.Equal(t, mockWalletStore, store)
	assert.Equal(t, 1, walletMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, tmsID, walletMgr.StoreServiceByTMSIdArgsForCall(0))
}

// TestDBStorageProvider_WalletStore_Error verifies that WalletStore properly
// propagates errors from the underlying wallet store service manager.
func TestDBStorageProvider_WalletStore_Error(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	expectedErr := errors.New("wallet store error")
	walletMgr.StoreServiceByTMSIdReturns(nil, expectedErr)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)
	store, err := provider.WalletStore(tmsID)

	require.Error(t, err)
	assert.Nil(t, store)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, 1, walletMgr.StoreServiceByTMSIdCallCount())
}

// TestDBStorageProvider_IdentityStore_Success verifies that IdentityStore successfully
// retrieves an identity store service for a given TMS ID.
func TestDBStorageProvider_IdentityStore_Success(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	mockIdentityStore := &identitydb.StoreService{}
	identityMgr.StoreServiceByTMSIdReturns(mockIdentityStore, nil)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)
	store, err := provider.IdentityStore(tmsID)

	require.NoError(t, err)
	assert.Equal(t, mockIdentityStore, store)
	assert.Equal(t, 1, identityMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, tmsID, identityMgr.StoreServiceByTMSIdArgsForCall(0))
}

// TestDBStorageProvider_IdentityStore_Error verifies that IdentityStore properly
// propagates errors from the underlying identity store service manager.
func TestDBStorageProvider_IdentityStore_Error(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	expectedErr := errors.New("identity store error")
	identityMgr.StoreServiceByTMSIdReturns(nil, expectedErr)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)
	store, err := provider.IdentityStore(tmsID)

	require.Error(t, err)
	assert.Nil(t, store)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, 1, identityMgr.StoreServiceByTMSIdCallCount())
}

// TestDBStorageProvider_Keystore_Success verifies that Keystore successfully
// retrieves a keystore service for a given TMS ID.
func TestDBStorageProvider_Keystore_Success(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	mockKeyStore := &keystoredb.StoreService{}
	keystoreMgr.StoreServiceByTMSIdReturns(mockKeyStore, nil)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)
	store, err := provider.Keystore(tmsID)

	require.NoError(t, err)
	assert.Equal(t, mockKeyStore, store)
	assert.Equal(t, 1, keystoreMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, tmsID, keystoreMgr.StoreServiceByTMSIdArgsForCall(0))
}

// TestDBStorageProvider_Keystore_Error verifies that Keystore properly
// propagates errors from the underlying keystore service manager.
func TestDBStorageProvider_Keystore_Error(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	expectedErr := errors.New("keystore error")
	keystoreMgr.StoreServiceByTMSIdReturns(nil, expectedErr)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)
	store, err := provider.Keystore(tmsID)

	require.Error(t, err)
	assert.Nil(t, store)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, 1, keystoreMgr.StoreServiceByTMSIdCallCount())
}

// TestDBStorageProvider_MultipleOperations verifies that the provider can handle
// multiple operations across different TMS IDs and store types.
func TestDBStorageProvider_MultipleOperations(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

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

	mockWalletStore1 := &walletdb.StoreService{}
	mockWalletStore2 := &walletdb.StoreService{}
	mockIdentityStore1 := &identitydb.StoreService{}
	mockKeyStore1 := &keystoredb.StoreService{}

	// Configure mocks to return different stores for different TMS IDs
	walletMgr.StoreServiceByTMSIdReturnsOnCall(0, mockWalletStore1, nil)
	walletMgr.StoreServiceByTMSIdReturnsOnCall(1, mockWalletStore2, nil)
	identityMgr.StoreServiceByTMSIdReturns(mockIdentityStore1, nil)
	keystoreMgr.StoreServiceByTMSIdReturns(mockKeyStore1, nil)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)

	// Test multiple wallet stores
	store1, err := provider.WalletStore(tmsID1)
	require.NoError(t, err)
	assert.Equal(t, mockWalletStore1, store1)

	store2, err := provider.WalletStore(tmsID2)
	require.NoError(t, err)
	assert.Equal(t, mockWalletStore2, store2)

	// Test identity store
	identityStore, err := provider.IdentityStore(tmsID1)
	require.NoError(t, err)
	assert.Equal(t, mockIdentityStore1, identityStore)

	// Test keystore
	keyStore, err := provider.Keystore(tmsID1)
	require.NoError(t, err)
	assert.Equal(t, mockKeyStore1, keyStore)

	// Verify call counts
	assert.Equal(t, 2, walletMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, 1, identityMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, 1, keystoreMgr.StoreServiceByTMSIdCallCount())
}

// TestDBStorageProvider_WithEmptyTMSID verifies that the provider can handle
// requests with an empty TMS ID (edge case).
func TestDBStorageProvider_WithEmptyTMSID(t *testing.T) {
	identityMgr := &mock.IdentityStoreServiceManager{}
	walletMgr := &mock.WalletStoreServiceManager{}
	keystoreMgr := &mock.KeystoreStoreServiceManager{}

	emptyTMSID := token.TMSID{}

	mockWalletStore := &walletdb.StoreService{}
	mockIdentityStore := &identitydb.StoreService{}
	mockKeyStore := &keystoredb.StoreService{}

	walletMgr.StoreServiceByTMSIdReturns(mockWalletStore, nil)
	identityMgr.StoreServiceByTMSIdReturns(mockIdentityStore, nil)
	keystoreMgr.StoreServiceByTMSIdReturns(mockKeyStore, nil)

	provider := identity.NewDBStorageProvider(identityMgr, walletMgr, keystoreMgr)

	// Should still work with empty TMSID
	walletStore, err := provider.WalletStore(emptyTMSID)
	require.NoError(t, err)
	assert.NotNil(t, walletStore)

	identityStore, err := provider.IdentityStore(emptyTMSID)
	require.NoError(t, err)
	assert.NotNil(t, identityStore)

	keyStore, err := provider.Keystore(emptyTMSID)
	require.NoError(t, err)
	assert.NotNil(t, keyStore)

	// Verify all managers were called
	assert.Equal(t, 1, walletMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, 1, identityMgr.StoreServiceByTMSIdCallCount())
	assert.Equal(t, 1, keystoreMgr.StoreServiceByTMSIdCallCount())
}

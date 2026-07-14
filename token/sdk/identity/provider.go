/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identity

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/identitydb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/keystoredb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/walletdb"
)

// IdentityStoreServiceManager manages identity store services for different TMS instances.
// This interface is used for dependency injection and mock generation.
//
//go:generate counterfeiter -o mock/identity_store_service_manager.go --fake-name IdentityStoreServiceManager . IdentityStoreServiceManager
type IdentityStoreServiceManager interface {
	// StoreServiceByTMSId returns the identity store service for the given TMS ID.
	StoreServiceByTMSId(tmsID token.TMSID) (*identitydb.StoreService, error)
}

// WalletStoreServiceManager manages wallet store services for different TMS instances.
// This interface is used for dependency injection and mock generation.
//
//go:generate counterfeiter -o mock/wallet_store_service_manager.go --fake-name WalletStoreServiceManager . WalletStoreServiceManager
type WalletStoreServiceManager interface {
	// StoreServiceByTMSId returns the wallet store service for the given TMS ID.
	StoreServiceByTMSId(tmsID token.TMSID) (*walletdb.StoreService, error)
}

// KeystoreStoreServiceManager manages keystore services for different TMS instances.
// This interface is used for dependency injection and mock generation.
//
//go:generate counterfeiter -o mock/keystore_store_service_manager.go --fake-name KeystoreStoreServiceManager . KeystoreStoreServiceManager
type KeystoreStoreServiceManager interface {
	// StoreServiceByTMSId returns the keystore service for the given TMS ID.
	StoreServiceByTMSId(tmsID token.TMSID) (*keystoredb.StoreService, error)
}

// DBStorageProvider provides access to identity-related storage services.
// It aggregates identity, wallet, and keystore managers to provide a unified
// interface for accessing storage services by TMS ID.
type DBStorageProvider struct {
	identityStoreServiceManager IdentityStoreServiceManager
	walletStoreServiceManager   WalletStoreServiceManager
	keyStoreStoreServiceManager KeystoreStoreServiceManager
}

// NewDBStorageProvider creates a new DBStorageProvider with the given store service managers.
// This constructor is used by the dependency injection framework to wire up storage providers.
func NewDBStorageProvider(
	identityStoreServiceManager IdentityStoreServiceManager,
	walletStoreServiceManager WalletStoreServiceManager,
	keyStoreStoreServiceManager KeystoreStoreServiceManager,
) *DBStorageProvider {
	return &DBStorageProvider{
		identityStoreServiceManager: identityStoreServiceManager,
		walletStoreServiceManager:   walletStoreServiceManager,
		keyStoreStoreServiceManager: keyStoreStoreServiceManager,
	}
}

// WalletStore returns the wallet store service for the given TMS ID.
// It delegates to the underlying wallet store service manager.
func (s *DBStorageProvider) WalletStore(tmsID token.TMSID) (driver.WalletStoreService, error) {
	return s.walletStoreServiceManager.StoreServiceByTMSId(tmsID)
}

// IdentityStore returns the identity store service for the given TMS ID.
// It delegates to the underlying identity store service manager.
func (s *DBStorageProvider) IdentityStore(tmsID token.TMSID) (driver.IdentityStoreService, error) {
	return s.identityStoreServiceManager.StoreServiceByTMSId(tmsID)
}

// Keystore returns the keystore service for the given TMS ID.
// It delegates to the underlying keystore service manager.
func (s *DBStorageProvider) Keystore(tmsID token.TMSID) (driver.Keystore, error) {
	return s.keyStoreStoreServiceManager.StoreServiceByTMSId(tmsID)
}

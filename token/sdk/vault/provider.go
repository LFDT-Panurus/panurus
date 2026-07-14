/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package vault

import (
	"sync"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Provider creates and caches vault instances for TMS identifiers.
// It manages access to token, transaction, and audit storage services.
type Provider struct {
	tokenStoreServiceManager tokendb.StoreServiceManager
	ttxStoreServiceManager   ttxdb.StoreServiceManager
	auditStoreServiceManager auditdb.StoreServiceManager

	vaultCacheLock sync.RWMutex
	vaultCache     map[string]driver.Vault
}

// NewVaultProvider creates a vault provider with the given storage managers.
func NewVaultProvider(
	tokenStoreServiceManager tokendb.StoreServiceManager,
	ttxStoreServiceManager ttxdb.StoreServiceManager,
	auditStoreServiceManager auditdb.StoreServiceManager,
) *Provider {
	return &Provider{
		ttxStoreServiceManager:   ttxStoreServiceManager,
		tokenStoreServiceManager: tokenStoreServiceManager,
		auditStoreServiceManager: auditStoreServiceManager,
		vaultCache:               make(map[string]driver.Vault),
	}
}

// Vault returns a cached or newly created vault for the given TMS coordinates.
// It uses double-checked locking to ensure thread-safe cache access.
func (v *Provider) Vault(network string, channel string, namespace string) (driver.Vault, error) {
	k := network + channel + namespace
	// Check cache
	v.vaultCacheLock.RLock()
	res, ok := v.vaultCache[k]
	v.vaultCacheLock.RUnlock()
	if ok {
		return res, nil
	}

	// lock
	v.vaultCacheLock.Lock()
	defer v.vaultCacheLock.Unlock()

	// check cache again
	res, ok = v.vaultCache[k]
	if ok {
		return res, nil
	}

	tmsID := token.TMSID{
		Network:   network,
		Channel:   channel,
		Namespace: namespace,
	}
	tokenDB, err := v.tokenStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get token db")
	}
	ttxDB, err := v.ttxStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get ttx db")
	}
	auditDB, err := v.auditStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get audit db")
	}

	// Create new vault
	res, err = NewVault(tmsID, auditDB, ttxDB, tokenDB)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create new vault")
	}

	// update cache
	v.vaultCache[k] = res

	return res, nil
}

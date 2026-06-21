/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	selector "github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple/inmemory"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
)

// LockerProvider creates token lockers for the simple selector service.
// It manages transaction locking to prevent double-spending during token selection.
type LockerProvider struct {
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService]
	sleepTimeout           time.Duration
	validTxEvictionTimeout time.Duration
	lockerConfig           inmemory.LockerConfig
}

// NewLockerProvider creates a new locker provider with the given configuration.
func NewLockerProvider(
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService],
	sleepTimeout time.Duration,
	validTxEvictionTimeout time.Duration,
) *LockerProvider {
	return &LockerProvider{
		ttxStoreServiceManager: ttxStoreServiceManager,
		sleepTimeout:           sleepTimeout,
		validTxEvictionTimeout: validTxEvictionTimeout,
		lockerConfig:           inmemory.DefaultLockerConfig(),
	}
}

// NewLockerProviderWithConfig creates a new locker provider with custom locker configuration.
func NewLockerProviderWithConfig(
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService],
	sleepTimeout time.Duration,
	validTxEvictionTimeout time.Duration,
	lockerConfig inmemory.LockerConfig,
) *LockerProvider {
	return &LockerProvider{
		ttxStoreServiceManager: ttxStoreServiceManager,
		sleepTimeout:           sleepTimeout,
		validTxEvictionTimeout: validTxEvictionTimeout,
		lockerConfig:           lockerConfig,
	}
}

// New creates a locker for the specified network, channel, and namespace.
func (s *LockerProvider) New(network, channel, namespace string) (selector.Locker, error) {
	db, err := s.ttxStoreServiceManager.StoreServiceByTMSId(token.TMSID{
		Network:   network,
		Channel:   channel,
		Namespace: namespace,
	})
	if err != nil {
		return nil, err
	}

	return inmemory.NewLockerWithConfig(db, s.sleepTimeout, s.validTxEvictionTimeout, s.lockerConfig), nil
}

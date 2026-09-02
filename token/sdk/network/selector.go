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
	maxLocksPerTx          int // Resource limit: max locks per transaction
}

// NewLockerProvider creates a new locker provider with the given configuration.
func NewLockerProvider(
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService],
	sleepTimeout time.Duration,
	validTxEvictionTimeout time.Duration,
) *LockerProvider {
	return NewLockerProviderWithLimits(ttxStoreServiceManager, sleepTimeout, validTxEvictionTimeout, 0)
}

// NewLockerProviderWithLimits creates a new locker provider with resource limits.
func NewLockerProviderWithLimits(
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService],
	sleepTimeout time.Duration,
	validTxEvictionTimeout time.Duration,
	maxLocksPerTx int,
) *LockerProvider {
	return &LockerProvider{
		ttxStoreServiceManager: ttxStoreServiceManager,
		sleepTimeout:           sleepTimeout,
		validTxEvictionTimeout: validTxEvictionTimeout,
		maxLocksPerTx:          maxLocksPerTx,
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

	return inmemory.NewLockerWithLimits(db, s.sleepTimeout, s.validTxEvictionTimeout, s.maxLocksPerTx), nil
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokenlockdb

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
)

type StoreServiceManager db.StoreServiceManager[*StoreService]

func NewStoreServiceManager(cp db.ConfigService, drivers multiplexed.Driver) StoreServiceManager {
	return db.NewStoreServiceManager(cp, "tokenlockdb.persistence", drivers.NewTokenLock, newStoreService)
}

type StoreService struct{ driver.TokenLockStore }

func newStoreService(p driver.TokenLockStore) (*StoreService, error) {
	return &StoreService{TokenLockStore: p}, nil
}

// Unwrap returns the driver store this service wraps, so a caller can discover an optional
// capability on the implementation itself.
//
// This type embeds the driver.TokenLockStore *interface*, which promotes only that
// interface's method set: a capability a concrete driver offers beyond it - notably
// sherdlock.BatchLocker's LockBatch, which no driver interface declares because not every
// backend can batch - is invisible on *StoreService, so a type assertion against this
// wrapper silently reports the capability as absent. Callers that discover capabilities by
// assertion must therefore look through this method as well; see sherdlock.asBatchLocker.
//
// It tolerates a nil receiver and reports no store: capability discovery probes whatever
// Locker it is handed, and a typed-nil *StoreService inside an otherwise non-nil interface is
// a value it can legitimately be given - sherdlock's rate-limiting path builds a selector with
// no lock store at all, because the limiter must deny a request before the selector reaches
// one.
func (s *StoreService) Unwrap() driver.TokenLockStore {
	if s == nil {
		return nil
	}

	return s.TokenLockStore
}

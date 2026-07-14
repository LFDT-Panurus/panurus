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

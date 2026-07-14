/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package identitydb

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
)

type StoreServiceManager db.StoreServiceManager[*StoreService]

func NewStoreServiceManager(cp db.ConfigService, drivers multiplexed.Driver) StoreServiceManager {
	return db.NewStoreServiceManager(cp, "identitydb.persistence", drivers.NewIdentity, newStoreService)
}

// StoreService is a database that stores identity related information
type StoreService struct {
	driver.IdentityStore
}

func newStoreService(p driver.IdentityStore) (*StoreService, error) {
	return &StoreService{IdentityStore: p}, nil
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package keystoredb

import (
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
)

type StoreServiceManager db.StoreServiceManager[*StoreService]

func NewStoreServiceManager(cp db.ConfigService, drivers multiplexed.Driver) StoreServiceManager {
	return db.NewStoreServiceManager(cp, "keystoredb.persistence", drivers.NewKeyStore, newStoreService)
}

// StoreService is a database that stores identity related information
type StoreService struct {
	driver.KeyStore
}

func newStoreService(p driver.KeyStore) (*StoreService, error) {
	return &StoreService{KeyStore: p}, nil
}

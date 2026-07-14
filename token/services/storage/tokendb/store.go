/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokendb

import (
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/multiplexed"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type (
	//go:generate counterfeiter -o mock/token_store_service_manager.go --fake-name TokenStoreServiceManager . StoreServiceManager
	StoreServiceManager db.StoreServiceManager[*StoreService]
)

var managerType = reflect.TypeFor[*StoreServiceManager]()

func NewStoreServiceManager(cp db.ConfigService, drivers multiplexed.Driver) StoreServiceManager {
	return db.NewStoreServiceManager(cp, "tokendb.persistence", drivers.NewToken, newStoreService)
}

func GetByTMSId(sp token.ServiceProvider, tmsID token.TMSID) (*StoreService, error) {
	s, err := sp.GetService(managerType)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get manager service")
	}
	c, err := s.(StoreServiceManager).StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get db for tms [%s]", tmsID)
	}

	return c, nil
}

type TokenRecord = driver.TokenRecord

type Transaction struct {
	driver.TokenStoreTransaction
}

// StoreService is a database that stores token transactions related information
type StoreService struct {
	driver.TokenStore
}

func (d *StoreService) NewTransaction() (*Transaction, error) {
	tx, err := d.NewTokenDBTransaction()
	if err != nil {
		return nil, err
	}

	return &Transaction{TokenStoreTransaction: tx}, nil
}

func (d *StoreService) ContinueTransaction(tx driver.Transaction) (*Transaction, error) {
	ctx, err := d.ContinueTokenDBTransaction(tx)
	if err != nil {
		return nil, err
	}

	return &Transaction{TokenStoreTransaction: ctx}, nil
}

func newStoreService(p driver.TokenStore) (*StoreService, error) {
	return &StoreService{TokenStore: p}, nil
}

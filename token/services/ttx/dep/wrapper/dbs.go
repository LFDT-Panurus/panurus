/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
)

type TransactionDBProvider struct {
	storeServiceManager ttxdb.StoreServiceManager
}

func NewTransactionDBProvider(storeServiceManager ttxdb.StoreServiceManager) *TransactionDBProvider {
	return &TransactionDBProvider{storeServiceManager: storeServiceManager}
}

func (t *TransactionDBProvider) TransactionDB(tmsID token.TMSID) (dep.TransactionDB, error) {
	return t.storeServiceManager.StoreServiceByTMSId(tmsID)
}

type AuditDBProvider struct {
	storeServiceManager auditdb.StoreServiceManager
}

func NewAuditDBProvider(storeServiceManager auditdb.StoreServiceManager) *AuditDBProvider {
	return &AuditDBProvider{storeServiceManager: storeServiceManager}
}

func (t *AuditDBProvider) AuditDB(tmsID token.TMSID) (dep.AuditDB, error) {
	return t.storeServiceManager.StoreServiceByTMSId(tmsID)
}

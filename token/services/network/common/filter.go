/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"context"

	token3 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

type TransactionFilterProvider[F driver2.TransactionFilter] interface {
	New(tmsID token3.TMSID) (F, error)
}

// AcceptTxInDBFilterProvider provides instances of AcceptTxInDBsFilter based on the transaction db and audit db
// for a given TMS
type AcceptTxInDBFilterProvider struct {
	ttxStoreServiceManager   ttxdb.StoreServiceManager
	auditStoreServiceManager auditdb.StoreServiceManager
}

func NewAcceptTxInDBFilterProvider(ttxStoreServiceManager ttxdb.StoreServiceManager, auditStoreServiceManager auditdb.StoreServiceManager) *AcceptTxInDBFilterProvider {
	return &AcceptTxInDBFilterProvider{ttxStoreServiceManager: ttxStoreServiceManager, auditStoreServiceManager: auditStoreServiceManager}
}

func (p *AcceptTxInDBFilterProvider) New(tmsID token3.TMSID) (*AcceptTxInDBsFilter, error) {
	ttxDB, err := p.ttxStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get transaction db for [%s]", tmsID)
	}
	auditDB, err := p.auditStoreServiceManager.StoreServiceByTMSId(tmsID)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get audit db for [%s]", tmsID)
	}

	return &AcceptTxInDBsFilter{
		ttxDB:   ttxDB,
		auditDB: auditDB,
	}, nil
}

// AcceptTxInDBsFilter uses the transaction db and the audit db to decide if a given transaction needs
// to be further processed by Panurus upon a network event about its finality
type AcceptTxInDBsFilter struct {
	ttxDB   *ttxdb.StoreService
	auditDB *auditdb.StoreService
}

func (t *AcceptTxInDBsFilter) Accept(txID string, env []byte) (bool, error) {
	status, _, err := t.ttxDB.GetStatus(context.Background(), txID)
	if err != nil {
		return false, err
	}
	if status != ttxdb.Unknown {
		return true, nil
	}
	status, _, err = t.auditDB.GetStatus(context.Background(), txID)
	if err != nil {
		return false, err
	}

	return status != auditdb.Unknown, nil
}

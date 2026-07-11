/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	token3 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
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
		ttxBatcher:   newStatusBatcher(ttxDB),
		auditBatcher: newStatusBatcher(auditDB),
	}, nil
}

// AcceptTxInDBsFilter uses the transaction db and the audit db to decide if a given transaction needs
// to be further processed by Panurus upon a network event about its finality.
// FSC invokes Accept once per unknown transaction while committing a block, and
// several such calls can be in flight concurrently (bounded by the committer's
// parallelism); ttxBatcher/auditBatcher coalesce concurrent lookups landing
// within a short window into a single batched status query, instead of one
// query pair per transaction.
type AcceptTxInDBsFilter struct {
	ttxBatcher   *statusBatcher
	auditBatcher *statusBatcher
}

func (t *AcceptTxInDBsFilter) Accept(txID string, env []byte) (bool, error) {
	status, err := t.ttxBatcher.Get(txID)
	if err != nil {
		return false, err
	}
	if status != dbdriver.Unknown {
		return true, nil
	}
	status, err = t.auditBatcher.Get(txID)
	if err != nil {
		return false, err
	}

	return status != dbdriver.Unknown, nil
}

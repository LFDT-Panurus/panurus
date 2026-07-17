/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

// statusStore is the subset of a transaction/audit store service needed by
// the batching decorator: the batched status lookup plus the status
// listener registry and notification surface, which are passed through
// untouched.
type statusStore interface {
	statusFetcher
	AddStatusListener(txID string, ch chan db.TransactionStatusEvent)
	DeleteStatusListener(txID string, ch chan db.TransactionStatusEvent)
	ListenerTxIDs() []string
	NotifyStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string)
}

// batchingStatusDB wraps a transaction/audit store service so that
// single-tx GetStatus lookups issued by concurrent finality views are
// coalesced into one batched GetStatuses query per batch window. It
// implements both dep.TransactionDB and dep.AuditDB. The status message is
// not part of the batched query and is returned empty; the interfaces' only
// consumer (the ttx finality view) discards it.
type batchingStatusDB struct {
	store   statusStore
	batcher *statusBatcher
}

func newBatchingStatusDB(store statusStore) *batchingStatusDB {
	return &batchingStatusDB{store: store, batcher: newStatusBatcher(store)}
}

func (b *batchingStatusDB) GetStatus(ctx context.Context, txID string) (token.TxStatus, string, error) {
	status, err := b.batcher.Get(ctx, txID)

	return status, "", err
}

// GetStatuses is already a batch operation, so it goes straight to the store
// without passing through the single-tx batcher.
func (b *batchingStatusDB) GetStatuses(ctx context.Context, txIDs []string) (map[string]dbdriver.TxStatus, error) {
	return b.store.GetStatuses(ctx, txIDs)
}

func (b *batchingStatusDB) AddStatusListener(txID string, ch chan db.TransactionStatusEvent) {
	b.store.AddStatusListener(txID, ch)
}

func (b *batchingStatusDB) DeleteStatusListener(txID string, ch chan db.TransactionStatusEvent) {
	b.store.DeleteStatusListener(txID, ch)
}

func (b *batchingStatusDB) ListenerTxIDs() []string {
	return b.store.ListenerTxIDs()
}

func (b *batchingStatusDB) NotifyStatus(ctx context.Context, txID string, status dbdriver.TxStatus, message string) {
	b.store.NotifyStatus(ctx, txID, status, message)
}

type TransactionDBProvider struct {
	dbs lazy.Provider[token.TMSID, *batchingStatusDB]
}

func NewTransactionDBProvider(storeServiceManager ttxdb.StoreServiceManager) *TransactionDBProvider {
	// one decorator (hence one batcher) per TMS: all finality views for a
	// TMS share the same batcher, which is what makes coalescing effective
	return &TransactionDBProvider{dbs: lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (*batchingStatusDB, error) {
		store, err := storeServiceManager.StoreServiceByTMSId(tmsID)
		if err != nil {
			return nil, err
		}

		return newBatchingStatusDB(store), nil
	})}
}

func (t *TransactionDBProvider) TransactionDB(tmsID token.TMSID) (dep.TransactionDB, error) {
	return t.dbs.Get(tmsID)
}

type AuditDBProvider struct {
	dbs lazy.Provider[token.TMSID, *batchingStatusDB]
}

func NewAuditDBProvider(storeServiceManager auditdb.StoreServiceManager) *AuditDBProvider {
	// see NewTransactionDBProvider for why decorators are cached per TMS
	return &AuditDBProvider{dbs: lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (*batchingStatusDB, error) {
		store, err := storeServiceManager.StoreServiceByTMSId(tmsID)
		if err != nil {
			return nil, err
		}

		return newBatchingStatusDB(store), nil
	})}
}

func (t *AuditDBProvider) AuditDB(tmsID token.TMSID) (dep.AuditDB, error) {
	return t.dbs.Get(tmsID)
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	auditmock "github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/mock"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	ttxmock "github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStatusStore struct {
	fakeStatusFetcher
	added   []string
	deleted []string
}

func (f *fakeStatusStore) AddStatusListener(txID string, _ chan db.TransactionStatusEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, txID)
}

func (f *fakeStatusStore) DeleteStatusListener(txID string, _ chan db.TransactionStatusEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, txID)
}

func TestBatchingStatusDB_CoalescesConcurrentGetStatus(t *testing.T) {
	store := &fakeStatusStore{fakeStatusFetcher: fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{
		"tx1": dbdriver.Confirmed,
		"tx2": dbdriver.Pending,
	}}}
	d := newBatchingStatusDB(store)

	ids := []string{"tx1", "tx2"}
	results := make([]dbdriver.TxStatus, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			s, _, err := d.GetStatus(t.Context(), id)
			assert.NoError(t, err)
			results[i] = s
		}(i, id)
	}
	wg.Wait()

	assert.Equal(t, []dbdriver.TxStatus{dbdriver.Confirmed, dbdriver.Pending}, results)
	assert.Equal(t, 1, store.callCount(), "concurrent GetStatus calls should be coalesced into a single batched query")
}

func TestBatchingStatusDB_MissingTxIsUnknown(t *testing.T) {
	store := &fakeStatusStore{fakeStatusFetcher: fakeStatusFetcher{responses: map[string]dbdriver.TxStatus{}}}
	d := newBatchingStatusDB(store)

	status, msg, err := d.GetStatus(t.Context(), "missing")
	require.NoError(t, err)
	assert.Equal(t, dbdriver.Unknown, status)
	assert.Empty(t, msg)
}

func TestBatchingStatusDB_ListenersPassThrough(t *testing.T) {
	store := &fakeStatusStore{}
	d := newBatchingStatusDB(store)

	ch := make(chan db.TransactionStatusEvent, 1)
	d.AddStatusListener("tx1", ch)
	d.DeleteStatusListener("tx1", ch)

	assert.Equal(t, []string{"tx1"}, store.added)
	assert.Equal(t, []string{"tx1"}, store.deleted)
}

func TestTransactionDBProvider_CachesPerTMS(t *testing.T) {
	manager := &ttxmock.TTXStoreServiceManager{}
	provider := NewTransactionDBProvider(manager)

	tms1 := token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	tms2 := token.TMSID{Network: "n2", Channel: "c2", Namespace: "ns2"}

	db1a, err := provider.TransactionDB(tms1)
	require.NoError(t, err)
	db1b, err := provider.TransactionDB(tms1)
	require.NoError(t, err)
	db2, err := provider.TransactionDB(tms2)
	require.NoError(t, err)

	assert.Same(t, db1a, db1b, "same TMS ID must share one decorator (and one batcher)")
	assert.NotSame(t, db1a, db2, "different TMS IDs must not share a decorator")
	assert.Equal(t, 2, manager.StoreServiceByTMSIdCallCount(), "store must be resolved once per TMS ID")
}

func TestAuditDBProvider_CachesPerTMS(t *testing.T) {
	manager := &auditmock.AuditStoreServiceManager{}
	provider := NewAuditDBProvider(manager)

	tms := token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}

	db1a, err := provider.AuditDB(tms)
	require.NoError(t, err)
	db1b, err := provider.AuditDB(tms)
	require.NoError(t, err)

	assert.Same(t, db1a, db1b, "same TMS ID must share one decorator (and one batcher)")
	assert.Equal(t, 1, manager.StoreServiceByTMSIdCallCount())
}

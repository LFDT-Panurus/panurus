/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/memory"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// The tests in bounded_locker_test.go drive NewBoundedLocker against a
// FakeLocker, so they verify the wrapper's own accounting but not that the
// bound reaches the real lock store the loader actually wires it to
// (service.go: NewBoundedLocker(tokenLockStoreService, s.maxLocksPerTx), where
// tokenLockStoreService is a *tokenlockdb.StoreService). These tests close that
// gap by wrapping the production store type, backed by an in-memory SQL store,
// exactly as the loader does.

// newRealBoundedLocker builds an in-memory SQL-backed token lock store, wraps it
// in the same *tokenlockdb.StoreService the loader passes to NewBoundedLocker,
// and returns the bound locker together with the underlying store so a test can
// probe the store directly. Foreign keys on the lock table are satisfied by
// provisioning a token request and one stored token per index of producerTxID.
func newRealBoundedLocker(t *testing.T, maxLocksPerTx int, producerTxID string, indices ...uint64) (sherdlock.Locker, dbdriver.TokenLockStore) {
	t.Helper()

	drv := memory.NewDriver()
	tokenStore, err := drv.NewToken("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = tokenStore.Close() })
	lockStore, err := drv.NewTokenLock("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lockStore.Close() })
	txStore, err := drv.NewOwnerTransaction("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = txStore.Close() })

	ctx := t.Context()

	// Register the producer transaction so the (tx_id) foreign key is satisfied.
	txReq, err := txStore.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, txReq.AddTokenRequest(ctx, producerTxID, []byte(producerTxID+"_content"), nil, nil, tdriver.PPHash("tr")))
	require.NoError(t, txReq.Commit())

	// Store one owned token per index so the (tx_id, idx) foreign key is satisfied.
	tokenTx, err := tokenStore.NewTokenDBTransaction()
	require.NoError(t, err)
	for _, idx := range indices {
		require.NoError(t, tokenTx.StoreToken(ctx, dbdriver.TokenRecord{
			TxID:           producerTxID,
			Index:          idx,
			OwnerRaw:       []byte("owner1"),
			OwnerType:      "idemix",
			OwnerIdentity:  []byte("owner1"),
			Ledger:         []byte("ledger_data"),
			LedgerMetadata: []byte{},
			Quantity:       "0x64",
			Type:           "USD",
			Amount:         100,
			Owner:          true,
		}, []string{"owner1"}))
	}
	require.NoError(t, tokenTx.Commit())

	// Wrap the store in the exact production type before binding it.
	svc := &tokenlockdb.StoreService{TokenLockStore: lockStore}

	return sherdlock.NewBoundedLocker(svc, maxLocksPerTx), lockStore
}

// TestBoundedLocker_RealSQLStore_LocksReachStoreUntilLimit verifies, against the
// real SQL lock store, that locks under the ceiling are actually written to the
// store and the lock past the ceiling is rejected before the store is touched.
func TestBoundedLocker_RealSQLStore_LocksReachStoreUntilLimit(t *testing.T) {
	bl, store := newRealBoundedLocker(t, 2, "prod", 0, 1, 2)
	ctx := t.Context()

	require.NoError(t, bl.Lock(ctx, tokenID("prod", 0), "cons1", "wallet1"))
	require.NoError(t, bl.Lock(ctx, tokenID("prod", 1), "cons1", "wallet1"))

	// The two locks under the ceiling really hit the store: a competing consumer
	// cannot take the same rows (the lock table's primary key rejects it).
	require.True(t, errors.Is(store.Lock(ctx, tokenID("prod", 0), "other", "wallet1"), dbdriver.ErrTokenAlreadyLocked),
		"lock under the ceiling should have reached the real store")
	require.True(t, errors.Is(store.Lock(ctx, tokenID("prod", 1), "other", "wallet1"), dbdriver.ErrTokenAlreadyLocked),
		"lock under the ceiling should have reached the real store")

	// The third lock is over the ceiling, so the bound rejects it fast.
	err := bl.Lock(ctx, tokenID("prod", 2), "cons1", "wallet1")
	require.Error(t, err)
	require.True(t, errors.Is(err, token.SelectorRateLimited), "expected SelectorRateLimited, got: %v", err)

	// It never reached the store, so index 2 is still free to lock.
	require.NoError(t, store.Lock(ctx, tokenID("prod", 2), "other", "wallet1"),
		"lock over the ceiling must not have been written to the store")
}

// TestBoundedLocker_RealSQLStore_UnlockReleasesAndResets verifies that
// UnlockByTxID both releases the rows in the real store and resets the counter,
// so the consumer can lock again afterwards.
func TestBoundedLocker_RealSQLStore_UnlockReleasesAndResets(t *testing.T) {
	bl, store := newRealBoundedLocker(t, 1, "prod", 0, 1)
	ctx := t.Context()

	require.NoError(t, bl.Lock(ctx, tokenID("prod", 0), "cons1", "wallet1"))
	require.True(t, errors.Is(bl.Lock(ctx, tokenID("prod", 1), "cons1", "wallet1"), token.SelectorRateLimited))

	require.NoError(t, bl.UnlockByTxID(ctx, "cons1"))

	// The row was released in the store...
	require.NoError(t, store.Lock(ctx, tokenID("prod", 0), "other", "wallet1"),
		"UnlockByTxID should have released the lock row")
	// ...and the counter was reset, so the consumer has its full budget again.
	require.NoError(t, bl.Lock(ctx, tokenID("prod", 1), "cons1", "wallet1"),
		"UnlockByTxID should have reset the per-transaction counter")
}

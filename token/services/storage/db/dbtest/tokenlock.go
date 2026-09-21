/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dbtest

import (
	"math/big"
	"testing"
	"time"

	driver2 "github.com/LFDT-Panurus/panurus/token/driver"
	driver3 "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// agedLease is the leaseExpiry used for the aged-lease test.
const agedLease = time.Hour

func TokenLocksTest(t *testing.T, cfgProvider cfgProvider) {
	t.Helper()
	for _, c := range tokenLockDBCases {
		driver := cfgProvider(c.Name)

		// Create token store first to ensure the tokens table exists
		// This is required because token locks now have a foreign key constraint
		// referencing the tokens table
		tokenDB, err := driver.NewToken("", c.Name)
		if err != nil {
			t.Fatal(err)
		}

		tokenLockDB, err := driver.NewTokenLock("", c.Name)
		if err != nil {
			utils.IgnoreError(tokenDB.Close)
			t.Fatal(err)
		}
		tokenTransactionDB, err := driver.NewOwnerTransaction("", c.Name)
		if err != nil {
			utils.IgnoreError(tokenDB.Close)
			utils.IgnoreError(tokenLockDB.Close)
			t.Fatal(err)
		}
		t.Run(c.Name, func(xt *testing.T) {
			defer utils.IgnoreError(tokenDB.Close)
			defer utils.IgnoreError(tokenLockDB.Close)
			defer utils.IgnoreError(tokenTransactionDB.Close)
			c.Fn(xt, tokenDB, tokenLockDB, tokenTransactionDB)
		})
	}
}

var tokenLockDBCases = []struct {
	Name string
	Fn   func(*testing.T, driver3.TokenStore, driver3.TokenLockStore, driver3.TokenTransactionStore)
}{
	{"TestFully", TestFully},
	{"TestReleaseOnDeletedConsumer", TestReleaseOnDeletedConsumer},
	{"TestReleaseOnOrphanConsumer", TestReleaseOnOrphanConsumer},
	{"TestKeepOnDeletedProducer", TestKeepOnDeletedProducer},
	{"TestKeepSiblingIndices", TestKeepSiblingIndices},
	{"TestReleaseOnAgedLease", TestReleaseOnAgedLease},
	{"TestKeepFreshPendingLock", TestKeepFreshPendingLock},
	{"TestListLocks", TestListLocks},
}

func TestFully(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()

	// First, create a token request in the transaction store
	txReq, err := tokenTransactionDB.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, txReq.AddTokenRequest(ctx, "apple", []byte("apple_tx_content"), nil, nil, driver2.PPHash("tr")))
	require.NoError(t, txReq.Commit())

	// Create a token in the tokens table so the foreign key constraint is satisfied
	tokenTx, err := tokenDB.NewTokenDBTransaction()
	require.NoError(t, err)
	tokenRecord := driver3.TokenRecord{
		TxID:           "apple",
		Index:          0,
		OwnerRaw:       []byte("owner1"),
		OwnerType:      "idemix",
		OwnerIdentity:  []byte("owner1"),
		Ledger:         []byte("ledger_data"),
		LedgerMetadata: []byte{}, // Empty metadata
		Quantity:       "0x64",   // 100 in hex
		Type:           "USD",
		Amount:         big.NewInt(100),
		Owner:          true,
	}
	err = tokenTx.StoreToken(ctx, tokenRecord, []string{"owner1"})
	require.NoError(t, err, "Store token should succeed")
	require.NoError(t, tokenTx.Commit())

	// Lock the token - this will now succeed because the token exists in the tokens table
	err = tokenLockDB.Lock(ctx, &token.ID{TxId: "apple", Index: 0}, "pineapple", "owner1")
	require.NoError(t, err, "Lock should succeed")

	// Unlock the token by transaction ID
	err = tokenLockDB.UnlockByTxID(ctx, "pineapple")
	require.NoError(t, err, "Unlock should succeed")

	// Cleanup should work correctly
	require.NoError(t, tokenLockDB.Cleanup(ctx, 1*time.Second))
}

// longLease outlives any of these tests, so a Cleanup call using it can only collect
// locks through the status of their consuming transaction, never through lease ageing.
const longLease = time.Hour

// TestReleaseOnDeletedConsumer verifies that a lease is released as soon as the
// transaction that was going to spend the token is Deleted, rather than being held
// until the lease ages out. See #2018.
func TestReleaseOnDeletedConsumer(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	tokenID := token.ID{TxId: "producer", Index: 0}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "consumer")
	storeTokens(t, tokenDB, "producer", 0)
	require.NoError(t, tokenLockDB.Lock(ctx, &tokenID, "consumer", "owner1"))

	require.NoError(t, tokenTransactionDB.SetStatus(ctx, "consumer", driver3.Deleted, ""))
	require.NoError(t, tokenLockDB.Cleanup(ctx, longLease))

	requireLockReleased(t, tokenLockDB, tokenID)
}

// TestReleaseOnOrphanConsumer verifies that an Orphan consuming transaction releases
// its leases too, on the same tick as a Deleted one. See #2018.
func TestReleaseOnOrphanConsumer(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	tokenID := token.ID{TxId: "producer", Index: 0}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "consumer")
	storeTokens(t, tokenDB, "producer", 0)
	require.NoError(t, tokenLockDB.Lock(ctx, &tokenID, "consumer", "owner1"))

	require.NoError(t, tokenTransactionDB.SetStatus(ctx, "consumer", driver3.Orphan, ""))
	require.NoError(t, tokenLockDB.Cleanup(ctx, longLease))

	requireLockReleased(t, tokenLockDB, tokenID)
}

// TestKeepOnDeletedProducer verifies that the status of the transaction that created
// the locked token does not expire the lease: the consuming transaction is still in
// flight, so dropping its lock would let the token be selected twice. See #2018.
func TestKeepOnDeletedProducer(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	tokenID := token.ID{TxId: "producer", Index: 0}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "consumer")
	storeTokens(t, tokenDB, "producer", 0)
	require.NoError(t, tokenLockDB.Lock(ctx, &tokenID, "consumer", "owner1"))

	require.NoError(t, tokenTransactionDB.SetStatus(ctx, "producer", driver3.Deleted, ""))
	require.NoError(t, tokenLockDB.Cleanup(ctx, longLease))

	requireLockHeld(t, tokenLockDB, tokenID)
}

// TestKeepSiblingIndices verifies that expiring the lock of one output does not take
// the locks of the other outputs of the same transaction with it: the primary key of
// the lock table is (tx_id, idx), so cleanup must be scoped to both. See #2018.
func TestKeepSiblingIndices(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	expired := token.ID{TxId: "producer", Index: 0}
	live := token.ID{TxId: "producer", Index: 1}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "dead-consumer")
	addTokenRequest(t, tokenTransactionDB, "live-consumer")
	storeTokens(t, tokenDB, "producer", 0, 1)
	require.NoError(t, tokenLockDB.Lock(ctx, &expired, "dead-consumer", "owner1"))
	require.NoError(t, tokenLockDB.Lock(ctx, &live, "live-consumer", "owner1"))

	require.NoError(t, tokenTransactionDB.SetStatus(ctx, "dead-consumer", driver3.Deleted, ""))
	require.NoError(t, tokenLockDB.Cleanup(ctx, longLease))

	requireLockHeld(t, tokenLockDB, live)
	requireLockReleased(t, tokenLockDB, expired)
}

// TestReleaseOnAgedLease verifies the second expiry branch: a lock whose consuming
// transaction never reaches a terminal status is reclaimed once its lease is older
// than leaseExpiry.
//
// The lock is inserted with a backdated created_at so no sleep is needed.
// The threshold used in Cleanup (agedLease = 1h) is safely larger than any
// clock skew between the test process and the database.
func TestReleaseOnAgedLease(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	tokenID := token.ID{TxId: "producer", Index: 0}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "consumer")
	storeTokens(t, tokenDB, "producer", 0)
	require.NoError(t, tokenLockDB.LockAt(ctx, &tokenID, "consumer", "owner1", time.Now().Add(-2*agedLease)))
	require.NoError(t, tokenLockDB.Cleanup(ctx, agedLease))

	requireLockReleased(t, tokenLockDB, tokenID)
}

// TestKeepFreshPendingLock verifies that cleanup leaves alone a fresh lock whose
// consuming transaction is still pending - neither expiry branch applies to it.
func TestKeepFreshPendingLock(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	tokenID := token.ID{TxId: "producer", Index: 0}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "consumer")
	storeTokens(t, tokenDB, "producer", 0)
	require.NoError(t, tokenLockDB.Lock(ctx, &tokenID, "consumer", "owner1"))

	require.NoError(t, tokenLockDB.Cleanup(ctx, longLease))

	requireLockHeld(t, tokenLockDB, tokenID)
}

// TestListLocks verifies that ListLocks reports every held lock together with the
// current status of its consuming transaction, and that a released lock disappears
// from the listing. This is the diagnostic reader added for #2395.
func TestListLocks(t *testing.T, tokenDB driver3.TokenStore, tokenLockDB driver3.TokenLockStore, tokenTransactionDB driver3.TokenTransactionStore) {
	ctx := t.Context()
	held := token.ID{TxId: "producer", Index: 0}
	toRelease := token.ID{TxId: "producer", Index: 1}

	addTokenRequest(t, tokenTransactionDB, "producer")
	addTokenRequest(t, tokenTransactionDB, "pending-consumer")
	addTokenRequest(t, tokenTransactionDB, "settled-consumer")
	storeTokens(t, tokenDB, "producer", 0, 1)
	require.NoError(t, tokenLockDB.Lock(ctx, &held, "pending-consumer", "owner1"))
	require.NoError(t, tokenLockDB.Lock(ctx, &toRelease, "settled-consumer", "owner1"))

	locks, err := tokenLockDB.ListLocks(ctx)
	require.NoError(t, err)
	require.Len(t, locks, 2)

	byTokenID := map[token.ID]driver3.LockRecord{}
	for _, l := range locks {
		byTokenID[l.TokenID] = l
	}

	pending, ok := byTokenID[held]
	require.True(t, ok, "lock on %s should be reported", held)
	require.Equal(t, "pending-consumer", pending.ConsumerTxID)
	require.NotNil(t, pending.Status)
	require.Equal(t, driver3.Pending, *pending.Status)

	// Mark the second consumer settled: the lock is still held (this is the leak
	// TestListLocks is meant to surface - see mechanism 4 in #2395), and ListLocks
	// must show its consumer's terminal status rather than silently dropping it.
	require.NoError(t, tokenTransactionDB.SetStatus(ctx, "settled-consumer", driver3.Confirmed, ""))

	locks, err = tokenLockDB.ListLocks(ctx)
	require.NoError(t, err)
	require.Len(t, locks, 2)
	byTokenID = map[token.ID]driver3.LockRecord{}
	for _, l := range locks {
		byTokenID[l.TokenID] = l
	}
	settled, ok := byTokenID[toRelease]
	require.True(t, ok, "lock on %s should still be reported after its consumer settles", toRelease)
	require.NotNil(t, settled.Status)
	require.Equal(t, driver3.Confirmed, *settled.Status)

	// Releasing the lock removes it from the listing.
	require.NoError(t, tokenLockDB.UnlockByTxID(ctx, "settled-consumer"))
	locks, err = tokenLockDB.ListLocks(ctx)
	require.NoError(t, err)
	require.Len(t, locks, 1)
	require.Equal(t, held, locks[0].TokenID)
}

// addTokenRequest registers a token request for txID, so that its status can later be
// moved to a terminal one with SetStatus.
func addTokenRequest(t *testing.T, tokenTransactionDB driver3.TokenTransactionStore, txID string) {
	t.Helper()

	tx, err := tokenTransactionDB.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, tx.AddTokenRequest(t.Context(), txID, []byte(txID+"_tx_content"), nil, nil, driver2.PPHash("tr")))
	require.NoError(t, tx.Commit())
}

// storeTokens stores one owned token per index of txID, so that the (tx_id, idx)
// foreign key carried by the lock rows is satisfied.
func storeTokens(t *testing.T, tokenDB driver3.TokenStore, txID string, indices ...uint64) {
	t.Helper()

	tx, err := tokenDB.NewTokenDBTransaction()
	require.NoError(t, err)
	for _, index := range indices {
		require.NoError(t, tx.StoreToken(t.Context(), driver3.TokenRecord{
			TxID:           txID,
			Index:          index,
			OwnerRaw:       []byte("owner1"),
			OwnerType:      "idemix",
			OwnerIdentity:  []byte("owner1"),
			Ledger:         []byte("ledger_data"),
			LedgerMetadata: []byte{},
			Quantity:       "0x64",
			Type:           "USD",
			Amount:         big.NewInt(0x64),
			Owner:          true,
		}, []string{"owner1"}))
	}
	require.NoError(t, tx.Commit())
}

// requireLockHeld asserts that the lock on tokenID survived cleanup, via
// TokenLockStore.ListLocks (see #2395) rather than probing with a second Lock call.
func requireLockHeld(t *testing.T, tokenLockDB driver3.TokenLockStore, tokenID token.ID) {
	t.Helper()

	require.True(t, lockExists(t, tokenLockDB, tokenID),
		"lock on token %s should still be held", tokenID)
}

// requireLockReleased asserts that cleanup collected the lock on tokenID: the row is
// gone, so the token can be locked again.
func requireLockReleased(t *testing.T, tokenLockDB driver3.TokenLockStore, tokenID token.ID) {
	t.Helper()

	require.False(t, lockExists(t, tokenLockDB, tokenID),
		"lock on token %s should have been released", tokenID)
}

// lockExists reports whether tokenID appears among the currently held locks.
func lockExists(t *testing.T, tokenLockDB driver3.TokenLockStore, tokenID token.ID) bool {
	t.Helper()

	locks, err := tokenLockDB.ListLocks(t.Context())
	require.NoError(t, err)
	for _, l := range locks {
		if l.TokenID == tokenID {
			return true
		}
	}

	return false
}

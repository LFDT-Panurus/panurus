/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"math/big"
	"testing"
	"time"

	tokendriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/token"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestTokenLockStore_LockBatch_SkipLocked_SkipsRowLockedByConcurrentTx is the deterministic
// counterpart to sherdlock's statistical TestHotTokenContention/TestHotTokenContentionWideWindow
// benchmarks: it proves the actual FOR UPDATE SKIP LOCKED mechanism directly - a candidate row a
// concurrent transaction is currently holding open is skipped, not blocked or contended on -
// rather than inferring it from a conflict-rate number that, as those benchmarks show, does not
// move across strategies (SKIP LOCKED only helps against a genuinely simultaneous holder, not
// against an already-committed lock, which is the dominant conflict mode under load). It also
// verifies the design doc's caveat that this only pays off on a batch claim: a plain FOR UPDATE
// (no SKIP LOCKED) against the very same held row genuinely blocks, so the win below is a real
// avoidance, not a no-op against a lock nothing was contending on.
func TestTokenLockStore_LockBatch_SkipLocked_SkipsRowLockedByConcurrentTx(t *testing.T) {
	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("test-skiplocked-mechanism"))
	terminate, _, err := fscpostgres.StartPostgres(t.Context(), cfg, nil)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	t.Cleanup(terminate)

	db, err := sql.Open("pgx", cfg.DataSource())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rwdb := &common2.RWDB{ReadDB: db, WriteDB: db}

	tables, err := sqlcommon.GetTableNames("")
	require.NoError(t, err)

	tokenStore, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)
	require.NoError(t, err)
	require.NoError(t, tokenStore.CreateSchema())

	lockStore, err := newTokenLockStoreWithStrategy(rwdb, tables, sqlcommon.LockStrategySkipLocked)
	require.NoError(t, err)
	require.NoError(t, lockStore.CreateSchema())

	const claimTxID = "claiming-tx"
	ids := []*token.ID{
		{TxId: "issuing-tx", Index: 0},
		{TxId: "issuing-tx", Index: 1},
		{TxId: "issuing-tx", Index: 2},
	}
	txn, err := tokenStore.NewTokenDBTransaction()
	require.NoError(t, err)
	for _, id := range ids {
		require.NoError(t, txn.StoreToken(t.Context(), tokendriver.TokenRecord{
			TxID:           id.TxId,
			Index:          id.Index,
			IssuerRaw:      []byte{},
			OwnerRaw:       []byte{1, 2, 3},
			OwnerType:      "idemix",
			OwnerIdentity:  []byte{},
			Ledger:         []byte("ledger"),
			LedgerMetadata: []byte{},
			Quantity:       "0x1",
			Type:           "CHF",
			Amount:         big.NewInt(1),
			Owner:          true,
		}, []string{"alice"}))
	}
	require.NoError(t, txn.Commit())

	// Hold a row lock on ids[0] via a separate connection/transaction, simulating a concurrent
	// claimant mid-transaction on that exact row - the only case SKIP LOCKED is meant to help with.
	lockConn, err := db.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = lockConn.Close() })
	holder, err := lockConn.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	// #nosec G202 -- tables.Tokens is a trusted table name derived from this test's own
	// config, never from request input; the only per-request values (TxId, Index) are passed
	// as placeholders, never concatenated into the query text.
	forUpdateQuery := "SELECT 1 FROM " + tables.Tokens + " WHERE tx_id=$1 AND idx=$2 FOR UPDATE"
	_, err = holder.ExecContext(t.Context(), forUpdateQuery, ids[0].TxId, ids[0].Index)
	require.NoError(t, err)

	// Control: without SKIP LOCKED, a second FOR UPDATE on the same row genuinely blocks - this
	// confirms the held lock is real, so skipLocked's avoidance of it below is a real mechanism.
	blockedCtx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	blockErr := db.QueryRowContext(blockedCtx, forUpdateQuery, ids[0].TxId, ids[0].Index).Scan(new(int))
	require.ErrorIs(t, blockErr, context.DeadlineExceeded, "expected a plain FOR UPDATE to block on the held row lock")

	done := make(chan struct{})
	var won []*token.ID
	var lockErr error
	go func() {
		defer close(done)
		won, lockErr = lockStore.LockBatch(t.Context(), ids, claimTxID, "wallet")
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("LockBatch under skipLocked blocked instead of skipping past the row a concurrent transaction holds")
	}
	require.NoError(t, holder.Rollback())
	require.NoError(t, lockErr)

	wonSet := make(map[token.ID]struct{}, len(won))
	for _, id := range won {
		wonSet[*id] = struct{}{}
	}
	_, wonLocked := wonSet[*ids[0]]
	require.False(t, wonLocked, "skipLocked must not claim a row a concurrent transaction is currently holding")
	require.Contains(t, wonSet, *ids[1])
	require.Contains(t, wonSet, *ids[2])
}

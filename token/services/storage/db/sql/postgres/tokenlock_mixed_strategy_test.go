/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"fmt"
	"math/big"
	"sync"
	"testing"

	tokendriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestTokenLockStore_MixedStrategy_RollingDeploy exercises the scenario Phase 6's docs claim
// but never tested (docs/services/selector.md, common5.LockStrategyOnConflict's doc comment):
// a rolling deploy where some replicas are still running the old LockStrategyInsert code while
// others have already upgraded to a new strategy (LockStrategySkipLocked here, standing in for
// either non-default choice - the ON CONFLICT DO NOTHING mechanics it shares with
// LockStrategyOnConflict are what actually matters, not SKIP LOCKED's row-skipping specifically).
// Two TokenLockStore instances share one underlying rwdb/tables - exactly what a rolling deploy
// looks like from the database's point of view, since every replica points at the same schema
// regardless of which code version it is running - and race concurrently to lock the same
// token. Unlike TestTokenLockStore_LockBatch_SkipLocked_SkipsRowLockedByConcurrentTx, which
// pins SKIP LOCKED's skip-not-block mechanism using a deliberately held row lock, this test
// does not need to force any particular interleaving: the property under test - the table's
// unique constraint on (tx_id, idx) admits exactly one winner no matter how two differently-
// strategized clients interleave - holds for every interleaving, so genuine goroutine
// concurrency (a start barrier, not a controlled ordering) is enough to exercise it.
func TestTokenLockStore_MixedStrategy_RollingDeploy(t *testing.T) {
	cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("test-mixed-strategy"))
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

	// oldReplica and newReplica share the same rwdb/tables, mirroring a rolling deploy where
	// every replica points at one schema regardless of which code version it runs.
	oldReplica, err := newTokenLockStoreWithStrategy(rwdb, tables, sqlcommon.LockStrategyInsert)
	require.NoError(t, err)
	require.NoError(t, oldReplica.CreateSchema())

	newReplica, err := newTokenLockStoreWithStrategy(rwdb, tables, sqlcommon.LockStrategySkipLocked)
	require.NoError(t, err)

	const numRaces = 10
	ids := make([]*token.ID, numRaces)
	txn, err := tokenStore.NewTokenDBTransaction()
	require.NoError(t, err)
	for i := range numRaces {
		id := &token.ID{TxId: fmt.Sprintf("issuing-tx-%d", i), Index: 0}
		ids[i] = id
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

	for i, id := range ids {
		t.Run(fmt.Sprintf("race-%d", i), func(t *testing.T) {
			var (
				wg               sync.WaitGroup
				start            = make(chan struct{})
				oldErr, newErr   error
				oldTxID, newTxID = fmt.Sprintf("old-replica-tx-%d", i), fmt.Sprintf("new-replica-tx-%d", i)
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				oldErr = oldReplica.Lock(t.Context(), id, oldTxID, "wallet")
			}()
			go func() {
				defer wg.Done()
				<-start
				newErr = newReplica.Lock(t.Context(), id, newTxID, "wallet")
			}()
			close(start)
			wg.Wait()

			// Exactly one of the two racing Lock calls must win, regardless of which
			// strategy's code path got there first: the table's unique constraint on
			// (tx_id, idx) is the real source of truth, and both strategies are required
			// to surface a lost race as driver.ErrTokenAlreadyLocked rather than a distinct,
			// caller-visible error shape (see tokenlock.go's Lock doc comment).
			oldWon, newWon := oldErr == nil, newErr == nil
			require.NotEqual(t, oldWon, newWon, "expected exactly one of the two racing replicas to win the lock, got oldErr=%v newErr=%v", oldErr, newErr)
			if !oldWon {
				require.True(t, errors.Is(oldErr, tokendriver.ErrTokenAlreadyLocked), "expected the old (insert-strategy) replica's loss to surface as ErrTokenAlreadyLocked, got: %v", oldErr)
			}
			if !newWon {
				require.True(t, errors.Is(newErr, tokendriver.ErrTokenAlreadyLocked), "expected the new (skipLocked-strategy) replica's loss to surface as ErrTokenAlreadyLocked, got: %v", newErr)
			}

			// No corruption: exactly one row exists for this token in the shared lock table,
			// no matter which replica's strategy actually inserted it.
			// #nosec G202 -- tables.TokenLocks is a trusted table name derived from this
			// test's own config, never from request input; the only per-request values
			// (TxId, Index) are passed as placeholders, never concatenated into the query.
			var rowCount int
			countQuery := "SELECT COUNT(*) FROM " + tables.TokenLocks + " WHERE tx_id=$1 AND idx=$2"
			require.NoError(t, db.QueryRowContext(t.Context(), countQuery, id.TxId, id.Index).Scan(&rowCount))
			require.Equal(t, 1, rowCount, "expected exactly one lock row for a token raced by two differently-strategized replicas")
		})
	}
}

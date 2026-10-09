/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"database/sql"
	"math/big"
	"testing"

	tokendriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	sqlcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/common"
	"github.com/LFDT-Panurus/panurus/token/token"
	common2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/common"
	fscpostgres "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestTokenLockStore_LockBatch_ClassifiesUnwonCandidates pins the per-candidate answer
// LockBatch gives (driver.BatchLockOutcome) against a real Postgres, across both strategies
// that go through the batch claim. The selector's handling of the two unwon reasons is
// opposite - a contended candidate is retried and keeps the caller backing off, a stale one is
// dropped for good and taken as proof the candidate cache is behind the store - so a
// misclassification here is not a reporting detail: it either strands a perfectly good token or
// spends a retry budget waiting for contention nobody is causing (#2395).
//
// One claim is made over three candidates covering all three verdicts at once, which is what
// makes it a classification test rather than three separate outcome tests: the statement has to
// get them right *in the same pass*, with the spendability scan and the claim's own source
// disagreeing about the third row.
func TestTokenLockStore_LockBatch_ClassifiesUnwonCandidates(t *testing.T) {
	for _, strategy := range []string{sqlcommon.LockStrategyOnConflict, sqlcommon.LockStrategySkipLocked} {
		t.Run(strategy, func(t *testing.T) {
			cfg := fscpostgres.DefaultConfig(fscpostgres.WithDBName("test-lockbatch-classify-" + strategy))
			startPostgres(t, cfg)

			db, err := sql.Open("pgx", cfg.DataSource())
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			rwdb := &common2.RWDB{ReadDB: db, WriteDB: db}

			tables, err := sqlcommon.GetTableNames("")
			require.NoError(t, err)

			tokenStore, err := sqlcommon.NewTokenStoreWithNotifier(db, db, tables, NewConditionInterpreter(), nil)
			require.NoError(t, err)
			require.NoError(t, tokenStore.CreateSchema())

			lockStore, err := newTokenLockStoreWithStrategy(rwdb, tables, strategy)
			require.NoError(t, err)
			require.NoError(t, lockStore.CreateSchema())

			var (
				free    = &token.ID{TxId: "issuing-tx", Index: 0}
				spent   = &token.ID{TxId: "issuing-tx", Index: 1}
				claimed = &token.ID{TxId: "issuing-tx", Index: 2}
			)
			ids := []*token.ID{free, spent, claimed}

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

			// `spent` is moved past by the store, exactly as a transaction spending it would:
			// the row survives, so the claim still joins to it and the verdict has to come from
			// the spendability predicate rather than from a missing row.
			deleteTxn, err := tokenStore.NewTokenDBTransaction()
			require.NoError(t, err)
			require.NoError(t, deleteTxn.Delete(t.Context(), *spent, "spending-tx"))
			require.NoError(t, deleteTxn.Commit())

			// `claimed` is locked by another consumer and committed, which is the dominant
			// conflict mode under load - not a row someone holds open mid-transaction.
			require.NoError(t, lockStore.Lock(t.Context(), claimed, "other-consumer-tx", "alice"))

			outcome, err := lockStore.LockBatch(t.Context(), ids, "claiming-tx", "alice")
			require.NoError(t, err)

			require.Equal(t, []*token.ID{free}, outcome.Won,
				"the one spendable, unlocked candidate must be the only one claimed")
			require.Equal(t, []*token.ID{spent}, outcome.Stale,
				"a token the store has moved past must be reported as no longer spendable, not as contended")
			// `claimed` appears in neither set: that absence is how a lost race is reported, and
			// asserting it explicitly is the point - were it to leak into Stale, the selector
			// would blacklist a token that becomes claimable again the moment the holder
			// releases it, and would invalidate a cache that is not behind the store.
			require.NotContains(t, outcome.Won, claimed)
			require.NotContains(t, outcome.Stale, claimed)
		})
	}
}

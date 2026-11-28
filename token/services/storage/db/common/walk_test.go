/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common_test

import (
	"context"
	"fmt"
	"math/big"
	"path"
	"testing"
	"time"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/sql/sqlite"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/multiplexed"
	fscSqlite "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeAdapter gives a driver store the method name the checkers read transactions through.
type storeAdapter struct {
	driver.TokenTransactionStore
}

func (s storeAdapter) Transactions(ctx context.Context, params driver.QueryTransactionsParams, pagination driver2.Pagination) (*driver2.PageIterator[*driver.TransactionRecord], error) {
	return s.QueryTransactions(ctx, params, pagination)
}

func addTransaction(t *testing.T, store driver.TokenTransactionStore, txID string, storedAt time.Time) {
	t.Helper()
	w, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, w.AddTokenRequest(t.Context(), txID, []byte("request "+txID), nil, nil, tdriver.PPHash("pp")))
	require.NoError(t, w.AddTransaction(t.Context(), driver.TransactionRecord{
		TxID: txID, ActionType: driver.Transfer, SenderEID: "issuer", RecipientEID: "alice",
		TokenType: "coin", Amount: big.NewInt(1), Timestamp: storedAt, Status: driver.Pending,
	}))
	require.NoError(t, w.Commit())
}

// TestWalkTransactionsIsStableUnderConcurrentWrites is the regression test for #2431: the
// history walk used offset pagination, so a transaction written while the walk was running
// shifted every row behind it, and the transaction at the next page boundary came back a
// second time (or, with a row going away, was skipped). Paging by a cursor on the last
// transaction read instead visits every transaction exactly once.
func TestWalkTransactionsIsStableUnderConcurrentWrites(t *testing.T) {
	d := sqlite.NewDriver(multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
		DataSource:   fmt.Sprintf("file:%s?_pragma=busy_timeout(20000)", path.Join(t.TempDir(), "db.sqlite")),
		TablePrefix:  "walk",
		MaxOpenConns: 10,
	}))
	store, err := d.NewOwnerTransaction("", "walk")
	require.NoError(t, err)
	t.Cleanup(func() { utils.IgnoreError(store.Close) })

	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	var existing []string
	for i := range 7 {
		txID := fmt.Sprintf("tx%d", i)
		addTransaction(t, store, txID, base.Add(time.Duration(i)*time.Minute))
		existing = append(existing, txID)
	}

	// The default order is newest first, so a transaction committed during the walk lands
	// ahead of its position, which is exactly what used to shift the pages.
	seen := map[string]int{}
	newOnes := 0
	err = common.WalkTransactionsForTest(t.Context(), storeAdapter{store}, 2, driver.QueryTransactionsParams{}, func(r *driver.TransactionRecord) error {
		seen[r.TxID]++
		if newOnes < 3 {
			newOnes++
			addTransaction(t, store, fmt.Sprintf("new%d", newOnes), time.Now().UTC())
		}

		return nil
	})
	require.NoError(t, err)

	for _, txID := range existing {
		assert.Equal(t, 1, seen[txID], "transaction [%s] must be visited exactly once", txID)
	}
}

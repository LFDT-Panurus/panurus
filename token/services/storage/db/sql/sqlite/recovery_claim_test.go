/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sqlite

import (
	"math/big"
	"testing"
	"time"

	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecoveryClaimDefaults checks the recovery claim calls of a store that persists no
// claims: renewing always succeeds, since nobody can take a claim over, and the Orphan
// promotion is guarded by the status alone, so it never overwrites an outcome that was
// written first.
func TestRecoveryClaimDefaults(t *testing.T) {
	ctx := t.Context()
	store, err := NewDriver(sqliteCfg(t.TempDir(), "recovery")).NewOwnerTransaction("", "recovery")
	require.NoError(t, err)
	t.Cleanup(func() { utils.IgnoreError(store.Close) })

	for _, txID := range []string{"pending", "confirmed"} {
		w, err := store.NewTransactionStoreTransaction()
		require.NoError(t, err)
		require.NoError(t, w.AddTokenRequest(ctx, txID, []byte("request"), nil, nil, tdriver.PPHash("pp")))
		require.NoError(t, w.AddTransaction(ctx, driver.TransactionRecord{
			TxID: txID, ActionType: driver.Transfer, SenderEID: "a", RecipientEID: "b",
			TokenType: "coin", Amount: big.NewInt(1), Timestamp: time.Now(), Status: driver.Pending,
		}))
		require.NoError(t, w.Commit())
	}
	require.NoError(t, store.SetStatus(ctx, "confirmed", driver.Confirmed, "confirmed by the ledger"))

	renewed, err := store.RenewRecoveryClaim(ctx, "pending", "anyone", time.Minute)
	require.NoError(t, err)
	assert.True(t, renewed)

	marked, err := store.MarkRecoveryOrphan(ctx, "confirmed", "owner", "never reached the ledger")
	require.NoError(t, err)
	assert.False(t, marked, "a transaction that is no longer Pending must not be marked Orphan")
	status, message, err := store.GetStatus(ctx, "confirmed")
	require.NoError(t, err)
	assert.Equal(t, driver.Confirmed, status)
	assert.Equal(t, "confirmed by the ledger", message)

	marked, err = store.MarkRecoveryOrphan(ctx, "pending", "owner", "never reached the ledger")
	require.NoError(t, err)
	assert.True(t, marked)
	status, message, err = store.GetStatus(ctx, "pending")
	require.NoError(t, err)
	assert.Equal(t, driver.Orphan, status)
	assert.Equal(t, "never reached the ledger", message)

	marked, err = store.MarkRecoveryOrphan(ctx, "pending", "owner", "again")
	require.NoError(t, err)
	assert.False(t, marked, "marking is a one-time transition out of Pending")
}

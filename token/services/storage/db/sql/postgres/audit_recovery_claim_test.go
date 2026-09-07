/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"math/big"
	"testing"
	"time"

	tokensdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/stretchr/testify/require"
)

// newAuditStore returns an AuditTransactionStore backed by the given connection string,
// with its schema already created.
func newAuditStore(t *testing.T, pgConnStr, name string) *AuditTransactionStore {
	t.Helper()

	storeInterface, err := NewDriver(postgresCfg(pgConnStr, name)).NewAuditTransaction("test", name)
	require.NoError(t, err)
	store, ok := storeInterface.(*AuditTransactionStore)
	require.True(t, ok)
	require.NoError(t, store.CreateSchema())

	return store
}

// addPendingAuditTx inserts a Pending audit transaction and backdates it so it falls
// inside the recovery claim window.
func addPendingAuditTx(t *testing.T, ctx context.Context, store *AuditTransactionStore, storedAt time.Time, txIDs ...string) {
	t.Helper()

	aw, err := store.NewTransactionStoreTransaction()
	require.NoError(t, err)

	for _, txID := range txIDs {
		require.NoError(t, aw.AddTokenRequest(ctx, txID, []byte("request"), nil, nil, []byte("hash")))
		require.NoError(t, aw.AddTransaction(ctx, tokensdriver.TransactionRecord{
			TxID:         txID,
			ActionType:   tokensdriver.Transfer,
			SenderEID:    "sender",
			RecipientEID: "recipient",
			TokenType:    "USD",
			Amount:       big.NewInt(100),
			Timestamp:    storedAt,
		}))
	}
	require.NoError(t, aw.Commit())

	ageRequests(t, ctx, store.claims, storedAt, txIDs...)
}

// TestAuditClaimPendingTransactions_Atomic is the audit-side counterpart of
// TestClaimPendingTransactions_Atomic. Before the audit store overrode
// ClaimPendingTransactions it inherited the permissive SELECT from sqlcommon, which
// persisted no claim: both replicas below would have received all five transactions and
// each would have processed the whole batch on every sweep.
func TestAuditClaimPendingTransactions_Atomic(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	ctx := context.Background()

	// Two stores over the same database, standing in for two replicas.
	store1 := newAuditStore(t, pgConnStr, "aud_atomic")
	store2 := newAuditStore(t, pgConnStr, "aud_atomic")

	now := time.Now().UTC()
	oldTime := now.Add(-10 * time.Minute)

	txIDs := []string{"atx1", "atx2", "atx3", "atx4", "atx5"}
	addPendingAuditTx(t, ctx, store1, oldTime, txIDs...)

	params := tokensdriver.RecoveryClaimParams{
		OlderThan:     now,
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Owner:         "instance1",
	}

	claimed1, err := store1.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, claimed1, len(txIDs), "first replica should claim every pending audit transaction")

	params.Owner = "instance2"
	claimed2, err := store2.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Empty(t, claimed2, "second replica must claim nothing, the rows are already claimed")
}

// TestAuditClaimPendingTransactions_Lease verifies an audit claim is exclusive until its
// lease expires, and reclaimable afterwards.
func TestAuditClaimPendingTransactions_Lease(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	ctx := context.Background()
	store := newAuditStore(t, pgConnStr, "aud_lease")

	now := time.Now().UTC()
	oldTime := now.Add(-10 * time.Minute)
	addPendingAuditTx(t, ctx, store, oldTime, "atx1")

	params := tokensdriver.RecoveryClaimParams{
		OlderThan:     now,
		LeaseDuration: 1 * time.Second,
		Limit:         10,
		Owner:         "instance1",
	}

	claimed, err := store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	params.Owner = "instance2"
	claimed, err = store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Empty(t, claimed, "another owner must not claim before the lease expires")

	require.Eventually(t, func() bool {
		claimed, err = store.ClaimPendingTransactions(ctx, params)

		return err == nil && len(claimed) == 1
	}, 10*time.Second, 250*time.Millisecond, "claim should become available once the lease expires")
}

// TestAuditReleaseRecoveryClaim verifies the audit store actually clears the claim, so the
// row is immediately available again rather than staying locked until the lease runs out.
// The inherited implementation is a no-op and would leave the claim in place.
//
// It also pins that the release does NOT stamp status_message with the caller's
// bookkeeping text. recoverTransaction (recovery/manager.go) releases every still-pending
// row with message "recovered successfully" because Recover returns nil for both a genuine
// success and a not-yet-finalized (Busy/Unknown) transaction; forwarding that text to
// status_message on the audit path would overwrite the real ledger-status reason on every
// sweep. See TestAuditReleaseRecoveryClaim_PreservesLedgerRejectionReason for the case that
// actually loses data if this regresses.
func TestAuditReleaseRecoveryClaim(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	ctx := context.Background()
	store := newAuditStore(t, pgConnStr, "aud_release")

	now := time.Now().UTC()
	oldTime := now.Add(-10 * time.Minute)
	addPendingAuditTx(t, ctx, store, oldTime, "atx1")

	params := tokensdriver.RecoveryClaimParams{
		OlderThan:     now,
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Owner:         "instance1",
	}

	claimed, err := store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	require.NoError(t, store.ReleaseRecoveryClaim(ctx, "atx1", "instance1", "recovered successfully"))

	// A different owner can claim it right away, well inside the original 5 minute lease.
	params.Owner = "instance2"
	claimed, err = store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "released claim should be immediately available to another owner")

	status, message, err := store.GetStatus(ctx, "atx1")
	require.NoError(t, err)
	require.Equal(t, tokensdriver.Pending, status)
	require.Empty(t, message, "audit release must not stamp status_message with claim bookkeeping text")
}

// TestAuditReleaseRecoveryClaim_PreservesLedgerRejectionReason mirrors the failure Akram
// verified against a live PostgreSQL 16 instance during round-3 review of PR #2167: claim a
// row, have finality logic record why the ledger rejected it (SetStatus to Deleted with a
// real reason), then release the claim the way recoverTransaction always does, with a generic
// "recovered successfully" bookkeeping message. Before this fix, the release unconditionally
// overwrote status_message, silently erasing the rejection reason from the audit trail.
func TestAuditReleaseRecoveryClaim_PreservesLedgerRejectionReason(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	ctx := context.Background()
	store := newAuditStore(t, pgConnStr, "aud_release_reason")

	now := time.Now().UTC()
	oldTime := now.Add(-10 * time.Minute)
	addPendingAuditTx(t, ctx, store, oldTime, "atx1")

	params := tokensdriver.RecoveryClaimParams{
		OlderThan:     now,
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Owner:         "i1",
	}

	claimed, err := store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	const rejectionReason = "ledger rejected: bad signature"
	require.NoError(t, store.SetStatus(ctx, "atx1", tokensdriver.Deleted, rejectionReason))

	// recoverTransaction always releases with a bookkeeping message once Recover returns,
	// regardless of what SetStatus already recorded.
	require.NoError(t, store.ReleaseRecoveryClaim(ctx, "atx1", "i1", "recovered successfully"))

	status, message, err := store.GetStatus(ctx, "atx1")
	require.NoError(t, err)
	require.Equal(t, tokensdriver.Deleted, status)
	require.Equal(t, rejectionReason, message, "the ledger rejection reason must survive claim release")
}

// TestAuditReleaseRecoveryClaim_WrongOwner verifies a replica cannot release a claim held
// by another replica.
func TestAuditReleaseRecoveryClaim_WrongOwner(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	ctx := context.Background()
	store := newAuditStore(t, pgConnStr, "aud_relowner")

	now := time.Now().UTC()
	oldTime := now.Add(-10 * time.Minute)
	addPendingAuditTx(t, ctx, store, oldTime, "atx1")

	params := tokensdriver.RecoveryClaimParams{
		OlderThan:     now,
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Owner:         "instance1",
	}

	claimed, err := store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	// Releasing under the wrong owner is not an error, it simply affects no rows.
	require.NoError(t, store.ReleaseRecoveryClaim(ctx, "atx1", "instance2", "should not apply"))

	params.Owner = "instance3"
	claimed, err = store.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Empty(t, claimed, "claim held by instance1 must survive a release attempt by instance2")
}

// TestAuditAndOwnerClaimsAreIndependent pins that the two stores keep their claims in
// their own requests tables. A claim taken on the audit side must not hide a pending row
// from the owner sweep, or vice versa.
func TestAuditAndOwnerClaimsAreIndependent(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	ctx := context.Background()

	auditStore := newAuditStore(t, pgConnStr, "aud_split")

	ownerInterface, err := NewDriver(postgresCfg(pgConnStr, "aud_split")).
		NewOwnerTransaction("test", "aud_split")
	require.NoError(t, err)
	ownerStore, ok := ownerInterface.(*TransactionStore)
	require.True(t, ok)
	require.NoError(t, ownerStore.CreateSchema())

	require.NotEqual(t, auditStore.claims.tables.Requests, ownerStore.claims.tables.Requests,
		"audit and owner stores must not share a requests table")

	now := time.Now().UTC()
	oldTime := now.Add(-10 * time.Minute)

	addPendingAuditTx(t, ctx, auditStore, oldTime, "atx1")

	aw, err := ownerStore.NewTransactionStoreTransaction()
	require.NoError(t, err)
	require.NoError(t, aw.AddTokenRequest(ctx, "otx1", []byte("request"), nil, nil, []byte("hash")))
	require.NoError(t, aw.AddTransaction(ctx, tokensdriver.TransactionRecord{
		TxID:         "otx1",
		ActionType:   tokensdriver.Transfer,
		SenderEID:    "sender",
		RecipientEID: "recipient",
		TokenType:    "USD",
		Amount:       big.NewInt(100),
		Timestamp:    oldTime,
	}))
	require.NoError(t, aw.Commit())
	ageRequests(t, ctx, ownerStore.claims, oldTime, "otx1")

	params := tokensdriver.RecoveryClaimParams{
		OlderThan:     now,
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Owner:         "instance1",
	}

	auditClaimed, err := auditStore.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, auditClaimed, 1)
	require.Equal(t, "atx1", auditClaimed[0].TxID)

	ownerClaimed, err := ownerStore.ClaimPendingTransactions(ctx, params)
	require.NoError(t, err)
	require.Len(t, ownerClaimed, 1, "owner sweep must be unaffected by the audit claim")
	require.Equal(t, "otx1", ownerClaimed[0].TxID)
}

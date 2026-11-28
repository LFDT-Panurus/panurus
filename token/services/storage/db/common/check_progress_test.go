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

// progressFixture is a real SQLite transaction store with a fixed point in time
// every stored transaction is placed relative to.
type progressFixture struct {
	t     *testing.T
	store driver.TokenTransactionStore
	// base is when the first transaction of a test was stored. The clock the
	// checkers see is set well past it, so the safety lag does not hold anything back
	// unless a test asks for it.
	base time.Time
	now  time.Time
}

func newProgressFixture(t *testing.T) *progressFixture {
	t.Helper()
	d := sqlite.NewDriver(multiplexed.MockTypeConfig(fscSqlite.Persistence, fscSqlite.Config{
		DataSource:   fmt.Sprintf("file:%s?_pragma=busy_timeout(20000)", path.Join(t.TempDir(), "db.sqlite")),
		TablePrefix:  "progress",
		MaxOpenConns: 10,
	}))
	store, err := d.NewOwnerTransaction("", "progress")
	require.NoError(t, err)
	t.Cleanup(func() { utils.IgnoreError(store.Close) })

	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)

	return &progressFixture{t: t, store: store, base: base, now: base.Add(24 * time.Hour)}
}

// add stores a transaction with two movement records, minutes after base, with the passed status.
func (f *progressFixture) add(txID string, minutes int, status driver.TxStatus) time.Time {
	f.t.Helper()
	storedAt := f.base.Add(time.Duration(minutes) * time.Minute)
	w, err := f.store.NewTransactionStoreTransaction()
	require.NoError(f.t, err)
	require.NoError(f.t, w.AddTokenRequest(f.t.Context(), txID, []byte("request "+txID), nil, nil, tdriver.PPHash("pp")))
	for _, recipient := range []string{"alice", "bob"} {
		require.NoError(f.t, w.AddTransaction(f.t.Context(), driver.TransactionRecord{
			TxID:         txID,
			ActionType:   driver.Transfer,
			SenderEID:    "issuer",
			RecipientEID: recipient,
			TokenType:    "coin",
			Amount:       big.NewInt(1),
			Timestamp:    storedAt,
			Status:       driver.Pending,
		}))
	}
	require.NoError(f.t, w.Commit())
	if status != driver.Pending {
		f.setStatus(txID, status)
	}

	return storedAt
}

func (f *progressFixture) setStatus(txID string, status driver.TxStatus) {
	f.t.Helper()
	require.NoError(f.t, f.store.SetStatus(f.t.Context(), txID, status, ""))
}

func (f *progressFixture) checkers() *common.DefaultCheckers {
	// a page size of 3 makes every walk below cross page boundaries, and splits the
	// two movement records of some transactions across them
	return common.NewResumableCheckersForTest(storeAdapter{f.store}, f.store, func() time.Time { return f.now }, 3)
}

// sweep runs one walk, records the resume point it earned as a successful sweep
// would, and returns the transactions it checked, in order.
func (f *progressFixture) sweep() []string {
	f.t.Helper()
	checkers := f.checkers()
	var checked []string
	next, err := checkers.WalkSinceProgress(f.t.Context(), func(txID string) error {
		checked = append(checked, txID)

		return nil
	})
	require.NoError(f.t, err)
	require.NotNil(f.t, next)
	checkers.SetNextProgress(next)
	require.NoError(f.t, checkers.CommitLocalCompletenessProgress(f.t.Context()))

	return checked
}

func (f *progressFixture) resumePoint() time.Time {
	f.t.Helper()
	resumeFrom, ok, err := f.store.GetCheckProgress(f.t.Context(), common.CheckerLocalCompleteness)
	require.NoError(f.t, err)
	require.True(f.t, ok, "a resume point must have been recorded")

	return resumeFrom
}

// TestLocalCompletenessOnlyWalksWhatIsNew is the point of the resume point: once
// a sweep has verified the history, the next one only looks at what was stored
// since, instead of re-parsing every transaction again.
func TestLocalCompletenessOnlyWalksWhatIsNew(t *testing.T) {
	f := newProgressFixture(t)
	f.add("tx1", 0, driver.Confirmed)
	f.add("tx2", 1, driver.Confirmed)
	f.add("tx3", 2, driver.Deleted)
	f.add("tx4", 3, driver.Orphan)
	f.add("tx5", 4, driver.Confirmed)

	assert.Equal(t, []string{"tx1", "tx2", "tx5"}, f.sweep(), "the first sweep walks the whole history, once per confirmed transaction")
	assert.Equal(t, f.now.Add(-common.ProgressSafetyLag), f.resumePoint())

	assert.Empty(t, f.sweep(), "nothing was stored since, so there is nothing to do")

	f.now = f.now.Add(time.Hour)
	f.add("tx6", 24*60+30, driver.Confirmed)
	assert.Equal(t, []string{"tx6"}, f.sweep(), "only the new transaction is walked")
}

// TestLocalCompletenessWaitsForPendingTransactions checks a transaction that is
// still pending holds the resume point back: it can still be confirmed, and then
// its tokens must be checked like any other.
func TestLocalCompletenessWaitsForPendingTransactions(t *testing.T) {
	f := newProgressFixture(t)
	f.add("tx1", 0, driver.Confirmed)
	pendingSince := f.add("tx2", 1, driver.Pending)
	f.add("tx3", 2, driver.Confirmed)

	assert.Equal(t, []string{"tx1", "tx3"}, f.sweep())
	assert.Equal(t, pendingSince, f.resumePoint(), "the resume point must stop at the pending transaction")

	assert.Equal(t, []string{"tx3"}, f.sweep(), "the walk restarts at the pending transaction")

	f.setStatus("tx2", driver.Confirmed)
	assert.Equal(t, []string{"tx2", "tx3"}, f.sweep(), "once confirmed, the transaction is checked")
	assert.Equal(t, f.now.Add(-common.ProgressSafetyLag), f.resumePoint())
	assert.Empty(t, f.sweep())
}

// TestLocalCompletenessKeepsRecentHistoryAhead checks the resume point stays the
// safety lag behind now, so a transaction that becomes visible late, with a
// stored_at older than rows already read, is still walked by the next sweep.
func TestLocalCompletenessKeepsRecentHistoryAhead(t *testing.T) {
	f := newProgressFixture(t)
	f.now = f.base.Add(time.Hour)
	f.add("tx1", 0, driver.Confirmed)
	f.add("tx2", 55, driver.Confirmed)

	assert.Equal(t, []string{"tx1", "tx2"}, f.sweep())
	assert.Equal(t, f.base.Add(50*time.Minute), f.resumePoint())

	// committed late, stored_at behind tx2, which the previous sweep already read
	f.add("tx-late", 52, driver.Confirmed)
	assert.Equal(t, []string{"tx-late", "tx2"}, f.sweep())
}

// TestLocalCompletenessRechecksOpenFindings checks the transactions a stored
// finding is about are checked again on every sweep, even behind the resume
// point. That is what lets a finding close once the token lands, and why the
// check can stay Complete without walking the whole history.
func TestLocalCompletenessRechecksOpenFindings(t *testing.T) {
	f := newProgressFixture(t)
	f.add("tx1", 0, driver.Confirmed)
	f.add("tx2", 1, driver.Confirmed)
	assert.Equal(t, []string{"tx1", "tx2"}, f.sweep())

	ctx := t.Context()
	require.NoError(t, f.store.UpsertFindings(ctx, []driver.FindingRecord{
		{Key: "k1", Checker: common.CheckerLocalCompleteness, Code: common.CodeTokenMissingLocally, Severity: int(common.SeverityCritical), TxID: "tx1", TokenID: "tx1:0", Message: "missing"},
		{Key: "k2", Checker: common.CheckerLocalCompleteness, Code: common.CodeTokenMissingLocally, Severity: int(common.SeverityCritical), TxID: "tx1", TokenID: "tx1:1", Message: "missing"},
		{Key: "k3", Checker: common.CheckerLocalCompleteness, Code: common.CodeCheckFailed, Severity: int(common.SeverityWarning), Message: "no tx"},
		{Key: "k4", Checker: common.CheckerTransactions, Code: common.CodeTxStatusMismatch, Severity: int(common.SeverityWarning), TxID: "tx2", Message: "another check"},
	}, f.now))

	f.now = f.now.Add(time.Hour)
	f.add("tx3", 24*60+30, driver.Confirmed)
	assert.Equal(t, []string{"tx1", "tx3"}, f.sweep(), "tx1 has open findings of this check and is checked once; tx2's finding belongs to another check")

	resolved, err := f.store.ResolveFindingsNotSeenSince(ctx, []string{common.CheckerLocalCompleteness}, f.now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(3), resolved)
	assert.Empty(t, f.sweep(), "a resolved finding is not checked again")
}

// TestLocalCompletenessResumePointNeverMovesBack checks a clock that went
// backwards does not make the next sweep start further back than the last one.
func TestLocalCompletenessResumePointNeverMovesBack(t *testing.T) {
	f := newProgressFixture(t)
	f.add("tx1", 0, driver.Confirmed)
	f.sweep()
	recorded := f.resumePoint()

	f.now = f.now.Add(-6 * time.Hour)
	f.sweep()
	assert.Equal(t, recorded, f.resumePoint())
}

// TestLocalCompletenessCommitsOnlyWhatARunEarned checks the resume point is only
// recorded when a run left one to commit, and only once.
func TestLocalCompletenessCommitsOnlyWhatARunEarned(t *testing.T) {
	f := newProgressFixture(t)
	ctx := t.Context()
	checkers := f.checkers()

	require.NoError(t, checkers.CommitLocalCompletenessProgress(ctx))
	_, ok, err := f.store.GetCheckProgress(ctx, common.CheckerLocalCompleteness)
	require.NoError(t, err)
	assert.False(t, ok, "nothing was earned, so nothing may be recorded")

	earned := f.base.Add(time.Hour)
	checkers.SetNextProgress(&earned)
	require.NoError(t, checkers.CommitLocalCompletenessProgress(ctx))
	assert.Equal(t, earned, f.resumePoint())

	require.NoError(t, f.store.SetCheckProgress(ctx, common.CheckerLocalCompleteness, f.base))
	require.NoError(t, checkers.CommitLocalCompletenessProgress(ctx))
	assert.Equal(t, f.base, f.resumePoint(), "a resume point is committed once, not again by a later commit")
}

func TestHasInconclusive(t *testing.T) {
	assert.False(t, common.HasInconclusive(nil))
	assert.False(t, common.HasInconclusive([]common.Finding{{Code: common.CodeTokenMissingLocally}}))
	assert.True(t, common.HasInconclusive([]common.Finding{{Code: common.CodeTokenMissingLocally}, {Code: common.CodeCheckFailed}}))
}

// TestFindingsServiceCommitProgress checks every check that keeps a resume point
// gets to record it, even when another one fails to.
func TestFindingsServiceCommitProgress(t *testing.T) {
	var committed []string
	commit := func(name string, err error) func(context.Context) error {
		return func(context.Context) error {
			committed = append(committed, name)

			return err
		}
	}
	service := common.NewFindingsService([]common.NamedFindingChecker{
		{Name: "a", Commit: commit("a", assert.AnError)},
		{Name: "no progress"},
		{Name: "b", Commit: commit("b", nil)},
	})

	err := service.CommitProgress(t.Context())
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, []string{"a", "b"}, committed)
}

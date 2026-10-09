/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality_test

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	drivermock "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver/mock"
	depmock "github.com/LFDT-Panurus/panurus/token/services/ttx/dep/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality/mock"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func noopTracer() trace.Tracer {
	return noop.NewTracerProvider().Tracer("")
}

// newTestListener builds a Listener wired with the given ttxDB mock.
// tokens.Service is nil because the test cases below never reach the token-append path.
func newTestListener(t *testing.T, db *mock.TransactionDB) *finality.Listener {
	t.Helper()

	return finality.NewListener(
		logging.MustGetLogger(),
		&depmock.Network{},
		"test-namespace",
		finality.NewTokenRequestHasher(&depmock.TokenManagementServiceProvider{}, token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}),
		db,
		nil,
		&mock.SelectorManagerProvider{},
		noopTracer(),
		nil,
	)
}

// newTestListenerWithSelectorManager builds a Listener wired with the given ttxDB mock,
// tokens service (may be nil when the test never reaches the token-append path) and
// selector-manager provider, so lock-release assertions can observe the Unlock calls.
func newTestListenerWithSelectorManager(
	t *testing.T,
	db *mock.TransactionDB,
	tokens *mock.TokensService,
	smProvider *mock.SelectorManagerProvider,
) *finality.Listener {
	t.Helper()

	return finality.NewListener(
		logging.MustGetLogger(),
		&depmock.Network{},
		"test-namespace",
		finality.NewTokenRequestHasher(&depmock.TokenManagementServiceProvider{}, token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}),
		db,
		tokens,
		smProvider,
		noopTracer(),
		nil,
	)
}

// TestOnStatus_ContextCanceledDuringRetry is the primary regression test.
//
// Setup: ttxDB.SetStatus always returns a transient error, so the inner retryRunner
// would spin forever under the old code (unbounded exponential backoff, no context check).
//
// The fix: RunWithContext is used, so canceling the context unblocks the sleeping
// retry loop and OnStatus returns promptly.
func TestOnStatus_ContextCanceledDuringRetry(t *testing.T) {
	var setCalls atomic.Int32
	db := &mock.TransactionDB{}
	db.SetStatusReturns(errors.New("db unavailable"))
	db.SetStatusCalls(func(_ context.Context, _ string, _ storage.TxStatus, _ string) error {
		setCalls.Add(1)

		return errors.New("db unavailable")
	})
	l := newTestListener(t, db)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)
		// network.Invalid → runOnStatus sets txStatus=Deleted, then calls SetStatus.
		// SetStatus always fails → retry loop engages.
		l.OnStatus(ctx, "tx1", network.Invalid, "validation failed", nil)
	}()

	// Allow at least one SetStatus call before canceling.
	require.Eventually(t, func() bool { return setCalls.Load() >= 1 }, time.Second, 10*time.Millisecond)
	cancel()

	select {
	case <-done:
		// good: OnStatus returned after context was canceled
	case <-time.After(5 * time.Second):
		t.Fatal("OnStatus did not return after context cancellation — worker goroutine would be permanently stuck")
	}
}

// TestOnStatus_SucceedsAfterTransientError verifies that OnStatus retries correctly
// and completes successfully once the transient error resolves.
func TestOnStatus_SucceedsAfterTransientError(t *testing.T) {
	var setCalls atomic.Int32
	db := &mock.TransactionDB{}
	db.SetStatusCalls(func(_ context.Context, _ string, _ storage.TxStatus, _ string) error {
		n := setCalls.Add(1)
		if n < 3 {
			return errors.New("transient db error")
		}

		return nil
	})
	l := newTestListener(t, db)

	done := make(chan struct{})

	go func() {
		defer close(done)
		l.OnStatus(t.Context(), "tx1", network.Invalid, "validation failed", nil)
	}()

	select {
	case <-done:
		assert.GreaterOrEqual(t, int(setCalls.Load()), 3, "should have retried at least 3 times")
	case <-time.After(5 * time.Second):
		t.Fatal("OnStatus did not complete after transient errors resolved")
	}
}

// TestOnStatus_CompletesImmediatelyOnSuccess verifies the happy path:
// when SetStatus succeeds on the first try, OnStatus returns without any retries.
func TestOnStatus_CompletesImmediatelyOnSuccess(t *testing.T) {
	var setCalls atomic.Int32
	db := &mock.TransactionDB{}
	db.SetStatusCalls(func(_ context.Context, _ string, _ storage.TxStatus, _ string) error {
		setCalls.Add(1)

		return nil
	})
	l := newTestListener(t, db)

	l.OnStatus(t.Context(), "tx1", network.Invalid, "invalid", nil)

	assert.Equal(t, int32(1), setCalls.Load())
}

// TestOnStatus_ConcurrentCallsAreIndependent verifies that concurrent OnStatus calls
// for different transactions do not interfere with each other.
func TestOnStatus_ConcurrentCallsAreIndependent(t *testing.T) {
	var mu sync.Mutex
	statusSet := map[string]bool{}

	db := &mock.TransactionDB{}
	db.SetStatusCalls(func(_ context.Context, txID string, _ storage.TxStatus, _ string) error {
		mu.Lock()
		statusSet[txID] = true
		mu.Unlock()

		return nil
	})
	l := newTestListener(t, db)

	txIDs := []string{"tx1", "tx2", "tx3", "tx4", "tx5"}

	var wg sync.WaitGroup

	for _, txID := range txIDs {
		wg.Add(1)

		go func(id string) {
			defer wg.Done()
			l.OnStatus(t.Context(), id, network.Invalid, "invalid", nil)
		}(txID)
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	for _, id := range txIDs {
		assert.True(t, statusSet[id], "tx %s should have had its status set", id)
	}
}

// TestOnStatus_PreCanceledContextReturnsImmediately verifies that a pre-canceled context
// causes OnStatus to return before the first retry sleep, not after it.
func TestOnStatus_PreCanceledContextReturnsImmediately(t *testing.T) {
	db := &mock.TransactionDB{}
	db.SetStatusReturns(errors.New("always fails"))
	l := newTestListener(t, db)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before calling OnStatus

	start := time.Now()
	l.OnStatus(ctx, "tx1", network.Invalid, "invalid", nil)
	elapsed := time.Since(start)

	// Should not block for the 1s initial retry delay.
	assert.Less(t, elapsed, 500*time.Millisecond,
		"OnStatus should return promptly when context is already canceled")
}

// TestOnStatus_StatusSetToDeletedForInvalidTx verifies that for an Invalid network
// status, the local DB is updated to Deleted.
func TestOnStatus_StatusSetToDeletedForInvalidTx(t *testing.T) {
	var capturedStatus storage.TxStatus
	db := &mock.TransactionDB{}
	db.SetStatusCalls(func(_ context.Context, _ string, s storage.TxStatus, _ string) error {
		capturedStatus = s

		return nil
	})
	l := newTestListener(t, db)

	l.OnStatus(t.Context(), "tx1", network.Invalid, "rejected", nil)

	require.Equal(t, storage.Deleted, capturedStatus,
		"an Invalid network status should map to Deleted in local storage")
}

// TestCommit_NotifiesConfirmed verifies that the confirmed path pushes the
// status event to waiters after the store transaction commits: the
// transactional SetStatus bypasses the store service, so without an explicit
// NotifyStatus the finality waiters would only wake on the fallback polling.
func TestCommit_NotifiesConfirmed(t *testing.T) {
	db := &mock.TransactionDB{}
	storeTx := &drivermock.TransactionStoreTransaction{}
	db.NewTransactionReturns(storeTx, nil)

	require.NoError(t, finality.Commit(t.Context(), logging.MustGetLogger(), &mock.TokensService{}, db, "tx1", nil))

	require.Equal(t, 1, storeTx.SetStatusCallCount())
	_, _, txStatus, _ := storeTx.SetStatusArgsForCall(0)
	assert.Equal(t, storage.Confirmed, txStatus)
	require.Equal(t, 1, storeTx.CommitCallCount())

	require.Equal(t, 1, db.NotifyStatusCallCount(), "commit must push the Confirmed event to waiters")
	_, notifiedTxID, notifiedStatus, _ := db.NotifyStatusArgsForCall(0)
	assert.Equal(t, "tx1", notifiedTxID)
	assert.Equal(t, storage.Confirmed, notifiedStatus)
}

// TestCommit_NoNotifyOnCommitFailure verifies no status event is pushed when
// the store transaction fails to commit.
func TestCommit_NoNotifyOnCommitFailure(t *testing.T) {
	db := &mock.TransactionDB{}
	storeTx := &drivermock.TransactionStoreTransaction{}
	storeTx.CommitReturns(errors.New("commit failed"))
	db.NewTransactionReturns(storeTx, nil)

	require.Error(t, finality.Commit(t.Context(), logging.MustGetLogger(), &mock.TokensService{}, db, "tx1", nil))
	assert.Equal(t, 0, db.NotifyStatusCallCount(), "a failed commit must not wake waiters")
}

// TestCommit_PublishesTokenEventsAfterCommit verifies that the token events produced by
// AppendValid are published by Commit, and only once the store transaction is committed:
// before that, the tokens they refer to may still be rolled back.
func TestCommit_PublishesTokenEventsAfterCommit(t *testing.T) {
	db := &mock.TransactionDB{}
	storeTx := &drivermock.TransactionStoreTransaction{}
	db.NewTransactionReturns(storeTx, nil)

	var publishedAfter []string
	tokens := &mock.TokensService{}
	tokens.AppendValidReturns(func(context.Context) {
		publishedAfter = append(publishedAfter, "publish")
	}, nil)
	db.NotifyStatusCalls(func(context.Context, string, storage.TxStatus, string) {
		publishedAfter = append(publishedAfter, "notify-status")
	})
	storeTx.CommitCalls(func() error {
		publishedAfter = append(publishedAfter, "commit")

		return nil
	})

	require.NoError(t, finality.Commit(t.Context(), logging.MustGetLogger(), tokens, db, "tx1", nil))

	// the token events go out after the commit, and before waiters are woken
	assert.Equal(t, []string{"commit", "publish", "notify-status"}, publishedAfter)
}

// TestCommit_NoTokenEventsWhenTransactionIsNotCommitted verifies that a transaction that
// never reaches the store publishes no token events: a subscriber must not observe tokens
// that were rolled back. This is the regression test for issue #2183 at the caller level.
func TestCommit_NoTokenEventsWhenTransactionIsNotCommitted(t *testing.T) {
	tests := []struct {
		name  string
		setup func(storeTx *drivermock.TransactionStoreTransaction)
	}{
		{
			name: "commit fails",
			setup: func(storeTx *drivermock.TransactionStoreTransaction) {
				storeTx.CommitReturns(errors.New("commit failed"))
			},
		},
		{
			name: "setting the status fails",
			setup: func(storeTx *drivermock.TransactionStoreTransaction) {
				storeTx.SetStatusReturns(errors.New("set status failed"))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &mock.TransactionDB{}
			storeTx := &drivermock.TransactionStoreTransaction{}
			db.NewTransactionReturns(storeTx, nil)
			test.setup(storeTx)

			published := 0
			tokens := &mock.TokensService{}
			tokens.AppendValidReturns(func(context.Context) { published++ }, nil)

			require.Error(t, finality.Commit(t.Context(), logging.MustGetLogger(), tokens, db, "tx1", nil))
			assert.Equal(t, 0, published, "a transaction that was not committed must publish nothing")
			assert.Equal(t, 1, storeTx.RollbackCallCount())
		})
	}
}

// TestOnStatus_ReleasesLocksAfterTerminalStatus is the Listener-side regression
// test for #2395 mechanism 4: once a transaction's status is terminal
// (Confirmed or Deleted), its selection locks must be released immediately
// rather than left for the 3-minute lease-expiry sweep.
func TestOnStatus_ReleasesLocksAfterTerminalStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "confirmed", status: network.Valid},
		{name: "invalid maps to deleted", status: network.Invalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &mock.TransactionDB{}
			storeTx := &drivermock.TransactionStoreTransaction{}
			db.NewTransactionReturns(storeTx, nil)

			tokens := &mock.TokensService{}
			msgToSign := []byte("message")
			expectedHashString := utils.Hashable(msgToSign).String()
			tokenRequestHash, err := base64.StdEncoding.DecodeString(expectedHashString)
			require.NoError(t, err)
			tokens.GetCachedTokenRequestReturns(&token.Request{}, msgToSign)
			tokens.AppendValidReturns(nil, nil)

			sm := &fakeSelectorManager{}
			smProvider := &mock.SelectorManagerProvider{}
			smProvider.SelectorManagerReturns(sm, nil)

			l := finality.NewListener(
				logging.MustGetLogger(),
				&depmock.Network{},
				"test-namespace",
				finality.NewTokenRequestHasher(&depmock.TokenManagementServiceProvider{}, token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}),
				db,
				tokens,
				smProvider,
				noopTracer(),
				nil,
			)

			txID := "tx-terminal"
			l.OnStatus(t.Context(), txID, test.status, "", tokenRequestHash)

			require.Equal(t, []string{txID}, sm.unlockCalls,
				"a transaction reaching a terminal status must release its selection locks (#2395 mechanism 4)")
		})
	}
}

// TestOnStatus_DoesNotReleaseLocksOnUnrecognizedStatus pins the default branch of
// runOnStatus: a status that is neither network.Valid/network.Invalid nor one of the
// in-flight network.Busy/network.Unknown states says nothing about where the transaction
// actually stands. Releasing its selection locks would let a concurrent Select hand the
// very same tokens to another transaction while this one may still be in flight, so the
// locks stay held and are left to the lease-expiry sweep — unlike a recognized terminal
// status whose persistence keeps failing, which does release
// (TestOnStatus_ReleasesLocksOnceOnRetryExhaustion).
func TestOnStatus_DoesNotReleaseLocksOnUnrecognizedStatus(t *testing.T) {
	db := &mock.TransactionDB{}

	sm := &fakeSelectorManager{}
	smProvider := &mock.SelectorManagerProvider{}
	smProvider.SelectorManagerReturns(sm, nil)

	l := finality.NewListener(
		logging.MustGetLogger(),
		&depmock.Network{},
		"test-namespace",
		finality.NewTokenRequestHasher(&depmock.TokenManagementServiceProvider{}, token.TMSID{Network: "n", Channel: "c", Namespace: "ns"}),
		db,
		nil,
		smProvider,
		noopTracer(),
		nil,
	)

	const unrecognizedStatus = 9999
	l.OnStatus(t.Context(), "tx-unrecognized-status", unrecognizedStatus, "", nil)

	require.Empty(t, sm.unlockCalls,
		"a status this listener cannot classify leaves the transaction's true state unknown: "+
			"releasing its selection locks could re-offer in-flight tokens to a concurrent Select")
	require.Zero(t, db.SetStatusCallCount(),
		"an unrecognized status must not be persisted as a terminal one")
}

// TestOnStatus_DoesNotRetryUnrecognizedStatus pins the second half of the same decision:
// runOnStatus cannot reclassify an unrecognized status by being called again with the same
// arguments, so ErrUnrecognizedStatus terminates the retry loop on the first attempt rather
// than burning MaxRetry attempts and their backoff sleeps. The retry runner's first delay is
// one second, so a retrying OnStatus could not return this quickly.
func TestOnStatus_DoesNotRetryUnrecognizedStatus(t *testing.T) {
	var smCalls atomic.Int32
	smProvider := &mock.SelectorManagerProvider{}
	smProvider.SelectorManagerCalls(func() (token.SelectorManager, error) {
		smCalls.Add(1)

		return &fakeSelectorManager{}, nil
	})

	l := newTestListenerWithSelectorManager(t, &mock.TransactionDB{}, nil, smProvider)

	start := time.Now()
	l.OnStatus(t.Context(), "tx-unrecognized-once", 9999, "", nil)

	require.Less(t, time.Since(start), time.Second,
		"an unrecognized status must terminate the retry loop immediately, not sleep through its budget")
	require.Zero(t, int(smCalls.Load()),
		"no selector manager should even be resolved for a transaction whose locks are not released")
}

// TestOnStatus_DoesNotReleaseLocksOnNonTerminalStatus pins the counterpart to
// TestOnStatus_ReleasesLocksOnUnrecognizedStatus: network.Busy and network.Unknown are
// legitimate transient, *non*-terminal states — fabricx's ListenerEvent.process
// (token/services/network/fabricx/finality/finality.go) forwards exactly these to
// OnStatus while a transaction is still pending — so releasing their selection locks
// would hand the same tokens to a concurrent Select while the first transaction is
// still mid-commit. TTXRecoveryHandler.applyFinalityLogic (recovery.go) already treats
// them that way; runOnStatus must match.
func TestOnStatus_DoesNotReleaseLocksOnNonTerminalStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "busy", status: network.Busy},
		{name: "unknown", status: network.Unknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &mock.TransactionDB{}

			sm := &fakeSelectorManager{}
			smProvider := &mock.SelectorManagerProvider{}
			smProvider.SelectorManagerReturns(sm, nil)

			l := newTestListenerWithSelectorManager(t, db, nil, smProvider)

			l.OnStatus(t.Context(), "tx-still-in-flight", test.status, "", nil)

			require.Empty(t, sm.unlockCalls,
				"status [%d] is a non-terminal, in-flight state: releasing its selection locks lets a "+
					"concurrent Select re-offer the same tokens to another transaction", test.status)
			require.Zero(t, db.SetStatusCallCount(),
				"a non-terminal status must not be persisted as a terminal one")
		})
	}
}

// TestOnStatus_ReleasesLocksOnceOnRetryExhaustion covers the third give-up path of the
// finality listener: a genuinely terminal ledger status whose local persistence keeps
// failing. Once the retryRunner's budget is exhausted OnStatus gives up for good, so the
// transaction's selection locks must be released there too — otherwise they sit until the
// lease-expiry sweep (#2395 mechanism 4), the very window OnError's doc comment argues
// must be closed. Exactly once, not once per retry attempt.
func TestOnStatus_ReleasesLocksOnceOnRetryExhaustion(t *testing.T) {
	var setCalls atomic.Int32
	db := &mock.TransactionDB{}
	db.SetStatusCalls(func(context.Context, string, storage.TxStatus, string) error {
		setCalls.Add(1)

		return errors.New("ttxdb unavailable")
	})

	sm := &fakeSelectorManager{}
	smProvider := &mock.SelectorManagerProvider{}
	smProvider.SelectorManagerReturns(sm, nil)

	l := newTestListenerWithSelectorManager(t, db, nil, smProvider)

	txID := "tx-terminal-but-unpersistable"
	l.OnStatus(t.Context(), txID, network.Invalid, "rejected", nil)

	require.GreaterOrEqual(t, int(setCalls.Load()), finality.MaxRetry,
		"the retry budget should have been exhausted")
	require.Equal(t, []string{txID}, sm.unlockCalls,
		"a terminal ledger status whose persistence keeps failing must still release its selection locks, exactly once")
}

// TestOnStatus_DoesNotReleaseLocksOnCanceledContext pins the fourth give-up path, the one
// that looks like retry exhaustion but is not: utils.RetryRunner.RunWithErrorsContext returns
// ctx.Err() as soon as its context is done, both before the first attempt and from the
// backoff sleep between attempts. That error is neither nil nor ErrUnrecognizedStatus, so it
// used to fall through to the release — classifying a shutdown or an interrupted notification
// as "recognized terminal status whose persistence kept failing", and releasing the selection
// locks of a transaction whose state was never established. An interruption is evidence of
// nothing, exactly like Busy/Unknown, an unrecognized status and OnError, so the locks stay
// held and the recovery and lease-expiry sweeps reclaim them.
func TestOnStatus_DoesNotReleaseLocksOnCanceledContext(t *testing.T) {
	t.Run("canceled before the first attempt", func(t *testing.T) {
		db := &mock.TransactionDB{}
		db.SetStatusReturns(errors.New("ttxdb unavailable"))

		sm := &fakeSelectorManager{}
		smProvider := &mock.SelectorManagerProvider{}
		smProvider.SelectorManagerReturns(sm, nil)

		l := newTestListenerWithSelectorManager(t, db, nil, smProvider)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		l.OnStatus(ctx, "tx-canceled-before-start", network.Invalid, "rejected", nil)

		require.Empty(t, sm.unlockCalls,
			"a context canceled before the retry loop ran establishes nothing about the transaction: "+
				"releasing its selection locks could re-offer in-flight tokens to a concurrent Select")
	})

	t.Run("canceled during the retry backoff", func(t *testing.T) {
		var setCalls atomic.Int32
		db := &mock.TransactionDB{}
		db.SetStatusCalls(func(context.Context, string, storage.TxStatus, string) error {
			setCalls.Add(1)

			return errors.New("ttxdb unavailable")
		})

		sm := &fakeSelectorManager{}
		smProvider := &mock.SelectorManagerProvider{}
		smProvider.SelectorManagerReturns(sm, nil)

		l := newTestListenerWithSelectorManager(t, db, nil, smProvider)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			l.OnStatus(ctx, "tx-canceled-mid-retry", network.Invalid, "rejected", nil)
		}()

		// Cancel once the loop is demonstrably inside its first backoff sleep, so the
		// give-up error is ctx.Err() rather than the exhausted retry budget.
		require.Eventually(t, func() bool { return setCalls.Load() >= 1 }, time.Second, 10*time.Millisecond)
		cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("OnStatus did not return after context cancellation")
		}

		require.Less(t, int(setCalls.Load()), finality.MaxRetry,
			"the retry budget must have been abandoned rather than exhausted, otherwise this case "+
				"is indistinguishable from TestOnStatus_ReleasesLocksOnceOnRetryExhaustion")
		require.Empty(t, sm.unlockCalls,
			"an interrupted notification is not a verdict: its selection locks stay held for the "+
				"recovery and lease-expiry sweeps")
	})
}

// TestOnStatus_ReleasesLocksWhenStoreErrorWrapsContextCanceled pins the other side of that
// decision: the give-up path inspects the listener's own context, not the error it was handed.
// A store can return an error that wraps context.Canceled or context.DeadlineExceeded from its
// own internal deadline while this listener's context is still live; that is an ordinary
// persistence failure on a recognized terminal status, so the retry budget really is exhausted
// and the locks must be released. Sniffing the error for context sentinels instead would
// silently reclassify it and reopen the #2395 mechanism-4 window on this path.
func TestOnStatus_ReleasesLocksWhenStoreErrorWrapsContextCanceled(t *testing.T) {
	db := &mock.TransactionDB{}
	db.SetStatusReturns(errors.Join(errors.New("query timed out"), context.Canceled))

	sm := &fakeSelectorManager{}
	smProvider := &mock.SelectorManagerProvider{}
	smProvider.SelectorManagerReturns(sm, nil)

	l := newTestListenerWithSelectorManager(t, db, nil, smProvider)

	txID := "tx-store-deadline"
	l.OnStatus(t.Context(), txID, network.Invalid, "rejected", nil)

	require.Equal(t, []string{txID}, sm.unlockCalls,
		"the listener's context was never canceled, so this is a recognized terminal status whose "+
			"persistence kept failing: its locks must still be released exactly once")
}

// TestOnError tests the OnError callback
// TestOnError pins what OnError must *not* do. An undeliverable finality event carries no
// verdict, so the handler records it and stops: it must not write a status, open a
// transaction or notify anybody, because any of those would commit the node to a verdict it
// does not have. (That it also does not release the transaction's selection locks is
// TestOnError_DoesNotReleaseLocks below.)
func TestOnError(t *testing.T) {
	db := &mock.TransactionDB{}
	listener := newTestListener(t, db)

	require.NotPanics(t, func() {
		listener.OnError(t.Context(), "test-tx-id", errors.New("test error"))
	})

	assert.Zero(t, db.SetStatusCallCount(), "an undeliverable event is not evidence of any status")
	assert.Zero(t, db.NewTransactionCallCount(), "nothing is persisted for a transaction with no verdict")
	assert.Zero(t, db.NotifyStatusCallCount(), "no listener may be told a status that was never established")
}

// TestOnError_DoesNotReleaseLocks pins OnError's side of the "only release on a status known
// to be terminal" rule. OnError means the finality notification could not be delivered, which
// says nothing about where the transaction stands — and on the EVM driver says outright that
// it could not be observed: manager.go calls OnError when every poll in the window failed to
// reach the chain, and when the watch ends with no verdict. Releasing there would hand the
// tokens of a possibly in-flight (or already mined) transaction to a concurrent Select, the
// same hazard that keeps Busy/Unknown and ErrUnrecognizedStatus from releasing. The recovery
// sweep, which re-derives the status from the ledger, and the lease-expiry sweep behind it are
// the backstops that do not need to know the verdict.
func TestOnError_DoesNotReleaseLocks(t *testing.T) {
	sm := &fakeSelectorManager{}
	smProvider := &mock.SelectorManagerProvider{}
	smProvider.SelectorManagerReturns(sm, nil)

	l := newTestListenerWithSelectorManager(t, &mock.TransactionDB{}, nil, smProvider)

	require.NotPanics(t, func() {
		l.OnError(t.Context(), "tx-undeliverable", errors.New("finality: could not reach the chain before the timeout"))
	})

	require.Empty(t, sm.unlockCalls,
		"an undeliverable notification leaves the transaction's status unknown: releasing its "+
			"selection locks could re-offer in-flight tokens to a concurrent Select")
}

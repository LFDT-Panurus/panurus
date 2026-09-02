/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	recovery2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery"
	mock2 "github.com/LFDT-Panurus/panurus/token/services/storage/services/recovery/mock"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingLogger wraps the package's no-op MockLogger and records Errorf
// calls, so the escalation tests can assert on when the alert fires without
// depending on the real zap-backed logger's output.
type recordingLogger struct {
	logging.MockLogger
	mu       sync.Mutex
	messages []string
}

func (l *recordingLogger) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

func (l *recordingLogger) errorMessages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.messages))
	copy(out, l.messages)

	return out
}

func TestNewManager(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                30 * time.Second,
		ScanInterval:       30 * time.Second,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      30 * time.Second,
		TransactionTimeout: time.Second,
		InstanceID:         "test-instance",
	}

	manager := recovery2.NewManager(
		logger,
		mockDB,
		mockHandler,
		config,
	)

	require.NotNil(t, manager)
}

func TestManager_StartStop(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                100 * time.Millisecond,
		ScanInterval:       100 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 500 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)

	manager := recovery2.NewManager(
		logger,
		mockDB,
		mockHandler,
		config,
	)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err)

	// Starting again should return error
	err = manager.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")

	// Wait a bit to let it scan at least once (accounting for jitter delay up to 1s)
	time.Sleep(1200 * time.Millisecond)

	// Stop the manager
	_ = manager.Stop()

	assert.GreaterOrEqual(t, mockDB.AcquireRecoveryLeadershipCallCount(), 1)
	assert.GreaterOrEqual(t, mockDB.ClaimPendingTransactionsCallCount(), 1)
}

func TestManager_RecoverTransaction(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                100 * time.Millisecond,
		ScanInterval:       100 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 500 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	// Create a pending transaction claim
	txRecord := &ttxdb.RecoveryClaim{
		TxID: "tx123",
	}

	leadership1 := &mock2.Leadership{}
	leadership1.CloseReturns(nil)
	leadership2 := &mock2.Leadership{}
	leadership2.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadership1, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership2, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)
	mockHandler.RecoverReturns(nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	manager := recovery2.NewManager(
		logger,
		mockDB,
		mockHandler,
		config,
	)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err)

	// Wait for recovery to happen (accounting for jitter delay up to 1s)
	time.Sleep(1300 * time.Millisecond)

	// Stop the manager
	_ = manager.Stop()

	// Verify the transaction was recovered
	assert.GreaterOrEqual(t, mockHandler.RecoverCallCount(), 1)
	_, txID := mockHandler.RecoverArgsForCall(0)
	assert.Equal(t, "tx123", txID)
	assert.GreaterOrEqual(t, mockDB.ReleaseRecoveryClaimCallCount(), 1)
}

func TestManager_SkipAlreadyRecovered(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                50 * time.Millisecond,
		ScanInterval:       50 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 500 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	// Create a pending transaction claim
	txRecord := &ttxdb.RecoveryClaim{
		TxID: "tx456",
	}

	leadershipA := &mock2.Leadership{}
	leadershipA.CloseReturns(nil)
	leadershipB := &mock2.Leadership{}
	leadershipB.CloseReturns(nil)
	leadershipC := &mock2.Leadership{}
	leadershipC.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadershipA, true, nil)
	mockDB.AcquireRecoveryLeadershipReturnsOnCall(1, leadershipB, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadershipC, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)
	mockHandler.RecoverReturns(nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	manager := recovery2.NewManager(
		logger,
		mockDB,
		mockHandler,
		config,
	)

	// Start the manager
	err := manager.Start()
	require.NoError(t, err)

	// Wait for multiple scan cycles (accounting for jitter delay up to 1s)
	time.Sleep(1200 * time.Millisecond)

	// Stop the manager
	_ = manager.Stop()

	// Verify Recover was called only once (not on subsequent scans because claim was released)
	assert.Equal(t, 1, mockHandler.RecoverCallCount())
}

// TestManager_PromoteOrphanOnNotFoundPastGracePeriod verifies that when the
// recovery handler returns a NotFound-shaped error and the row was stored more
// than NotFoundGracePeriod ago, the manager promotes the tx to storage.Orphan
// (not storage.Deleted) so it stops blocking the sweep queue while staying
// distinguishable from txs the ledger actively rejected.
func TestManager_PromoteOrphanOnNotFoundPastGracePeriod(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:             true,
		TTL:                 100 * time.Millisecond,
		ScanInterval:        100 * time.Millisecond,
		BatchSize:           100,
		WorkerCount:         1,
		LeaseDuration:       time.Second,
		TransactionTimeout:  500 * time.Millisecond,
		InstanceID:          "test-instance",
		NotFoundGracePeriod: 10 * time.Millisecond,
	}

	// stored_at well beyond the 10ms grace period so the promotion fires.
	txRecord := &ttxdb.RecoveryClaim{
		TxID:     "txOrphan",
		StoredAt: time.Now().Add(-time.Hour),
	}

	leadership1 := &mock2.Leadership{}
	leadership1.CloseReturns(nil)
	leadership2 := &mock2.Leadership{}
	leadership2.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadership1, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership2, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)
	// Match isNotFoundError: "tx not found" is the FSC sentinel substring.
	mockHandler.RecoverReturns(errors.New("rpc error: code = NotFound desc = tx not found"))
	mockDB.ReleaseRecoveryClaimReturns(nil)
	mockDB.SetStatusReturns(nil)

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)

	require.NoError(t, manager.Start())
	// Wait for the initial sweep (jitter up to 1s + handler invocation).
	time.Sleep(1300 * time.Millisecond)
	_ = manager.Stop()

	require.GreaterOrEqual(t, mockHandler.RecoverCallCount(), 1)
	require.Equal(t, 1, mockDB.SetStatusCallCount(), "expected exactly one SetStatus call for the orphan promotion")

	_, gotTxID, gotStatus, gotMsg := mockDB.SetStatusArgsForCall(0)
	assert.Equal(t, "txOrphan", gotTxID)
	assert.Equal(t, storage.Orphan, gotStatus, "orphan path must promote to storage.Orphan, not storage.Deleted")
	assert.Contains(t, gotMsg, "tx never reached ledger")
}

// TestManager_NoPromotionWhenGracePeriodDisabled verifies that NotFoundGracePeriod=0
// disables the orphan promotion entirely, even when the row is old and the
// handler returns NotFound. This is the documented opt-out from
// recovery.Config.NotFoundGracePeriod.
func TestManager_NoPromotionWhenGracePeriodDisabled(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:             true,
		TTL:                 100 * time.Millisecond,
		ScanInterval:        100 * time.Millisecond,
		BatchSize:           100,
		WorkerCount:         1,
		LeaseDuration:       time.Second,
		TransactionTimeout:  500 * time.Millisecond,
		InstanceID:          "test-instance",
		NotFoundGracePeriod: 0,
	}

	txRecord := &ttxdb.RecoveryClaim{
		TxID:     "txStillPending",
		StoredAt: time.Now().Add(-time.Hour),
	}

	leadership1 := &mock2.Leadership{}
	leadership1.CloseReturns(nil)
	leadership2 := &mock2.Leadership{}
	leadership2.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadership1, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership2, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)
	mockHandler.RecoverReturns(errors.New("rpc error: code = NotFound desc = tx not found"))
	mockDB.ReleaseRecoveryClaimReturns(nil)

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)

	require.NoError(t, manager.Start())
	time.Sleep(1300 * time.Millisecond)
	_ = manager.Stop()

	assert.Equal(t, 0, mockDB.SetStatusCallCount(), "grace period disabled should never call SetStatus")
}

// TestManager_StopDuringFanOutDoesNotDeadlock walks the exact failure scenario
// reported in issue #2038, step by step:
//
//  1. recoveryLoop's goroutine is mid-fan-out, with claims still queued to send
//     to work — forced here by returning 64 claims to a single worker that is
//     parked inside Recover, so the unbuffered send has no receiver.
//  2. Stop() cancels m.ctx — called from a goroutine so the test can observe it
//     hanging instead of hanging with it.
//  3. All WorkerCount workers observe ctx.Done() in their select and exit before
//     draining the remaining items from work — the worker is released after the
//     cancellation, so both its select arms are ready and it takes ctx.Done().
//  4. The fan-out loop's next `work <- claim` send now has no receiver and, when
//     unguarded, blocks forever.
//  5. That send sits in the goroutine wg.Done() is deferred on, so the deferred
//     call never runs.
//  6. Stop()'s m.wg.Wait() — held under m.mu — hangs indefinitely, and every
//     later Start()/Stop() deadlocks on m.mu; asserted by the final restart.
//
// Verified to fail (10s timeout at step 6) against the unguarded send and to
// pass with the ctx.Done()-guarded fan-out.
func TestManager_StopDuringFanOutDoesNotDeadlock(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                10 * time.Millisecond,
		ScanInterval:       10 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 500 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	// Step 1: many more claims than workers, so the fan-out loop is guaranteed to
	// still have undispatched claims when the context is cancelled. 64 also makes
	// it statistically certain (1 - 2^-63) that the worker's select picks
	// ctx.Done() before draining the batch on its own.
	const claimCount = 64
	records := make([]*ttxdb.RecoveryClaim, claimCount)
	for i := range records {
		records[i] = &ttxdb.RecoveryClaim{TxID: "tx" + strconv.Itoa(i)}
	}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturns(records, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	var once sync.Once
	inWorker := make(chan struct{})
	release := make(chan struct{})
	mockHandler.RecoverStub = func(_ context.Context, _ string) error {
		// Hold the single worker inside Recover so the fan-out loop is parked
		// on its send, then let it go only after Stop() cancelled the context.
		once.Do(func() {
			close(inWorker)
			<-release
		})

		return nil
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	select {
	case <-inWorker:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the recovery sweep to reach a worker")
	}

	// Step 2: cancel m.ctx via Stop() while the fan-out is parked on its send.
	stopped := make(chan error, 1)
	go func() {
		stopped <- manager.Stop()
	}()

	// Step 3: let Stop() cancel the context first, then unblock the worker so it
	// returns to its select with both ctx.Done() and the fan-out send ready.
	time.Sleep(50 * time.Millisecond)
	close(release)

	// Steps 4-5-6: with the unguarded send this never returns.
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() deadlocked while the sweep was fanning out claims")
	}

	// Step 6, second half: the manager must not be wedged — m.mu was released, so
	// a subsequent Start()/Stop() pair still works.
	require.NoError(t, manager.Start())
	require.NoError(t, manager.Stop())
}

// TestManager_PromoteOrphanOnNoSuchTransactionID verifies that the
// "no such transaction ID" error message returned by the Fabric-X ledger
// (fabric-smart-client/platform/fabricx/core/ledger/ledger.go) is recognised
// as a NotFound error, so the manager promotes the tx to storage.Orphan after
// the NotFoundGracePeriod has elapsed.
func TestManager_PromoteOrphanOnNoSuchTransactionID(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:             true,
		TTL:                 100 * time.Millisecond,
		ScanInterval:        100 * time.Millisecond,
		BatchSize:           100,
		WorkerCount:         1,
		LeaseDuration:       time.Second,
		TransactionTimeout:  500 * time.Millisecond,
		InstanceID:          "test-instance",
		NotFoundGracePeriod: 10 * time.Millisecond,
	}

	// stored_at well beyond the 10ms grace period so the promotion fires.
	txRecord := &ttxdb.RecoveryClaim{
		TxID:     "txNoSuchTx",
		StoredAt: time.Now().Add(-time.Hour),
	}

	leadership1 := &mock2.Leadership{}
	leadership1.CloseReturns(nil)
	leadership2 := &mock2.Leadership{}
	leadership2.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadership1, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership2, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)
	// Use the Fabric-X ledger sentinel substring directly.
	mockHandler.RecoverReturns(errors.New("Failed to get transaction with id d48c4, error no such transaction ID [d48c] in index"))
	mockDB.ReleaseRecoveryClaimReturns(nil)
	mockDB.SetStatusReturns(nil)

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)

	require.NoError(t, manager.Start())
	// Wait for the initial sweep (jitter up to 1s + handler invocation).
	time.Sleep(1300 * time.Millisecond)
	_ = manager.Stop()

	require.GreaterOrEqual(t, mockHandler.RecoverCallCount(), 1)
	require.Equal(t, 1, mockDB.SetStatusCallCount(), "expected exactly one SetStatus call for the orphan promotion")

	_, gotTxID, gotStatus, gotMsg := mockDB.SetStatusArgsForCall(0)
	assert.Equal(t, "txNoSuchTx", gotTxID)
	assert.Equal(t, storage.Orphan, gotStatus, "orphan path must promote to storage.Orphan, not storage.Deleted")
	assert.Contains(t, gotMsg, "tx never reached ledger")
}

// TestManager_TransactionTimeoutBoundsHungRecover walks the scenario from
// issue #2192: a Recover call that never returns on its own (a hung status
// query). Without a per-transaction deadline, the worker would block forever
// and, since leadership is held for the whole sweep, no replica would ever
// recover another pending transaction. The stub here honours ctx.Done() the
// way a well-behaved caller would, so this proves the manager derives a
// bounded context for the call; it does not by itself prove the manager
// survives a handler that ignores that context entirely, see
// TestManager_TransactionTimeoutBoundsRecoverIgnoringContext for that, which
// is the shape of the real hang this issue is about (FSC's fabric
// Ledger.GetTransactionByID takes no context at all).
func TestManager_TransactionTimeoutBoundsHungRecover(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                10 * time.Millisecond,
		ScanInterval:       100 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 50 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	txRecord := &ttxdb.RecoveryClaim{TxID: "txHung"}

	leadership1 := &mock2.Leadership{}
	leadership1.CloseReturns(nil)
	leadership2 := &mock2.Leadership{}
	leadership2.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturnsOnCall(0, leadership1, true, nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership2, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	// Simulates a hung network call: only returns once its context is cancelled.
	mockHandler.RecoverStub = func(ctx context.Context, _ string) error {
		<-ctx.Done()

		return ctx.Err()
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	// Poll for leadership having been released and reacquired at least once,
	// rather than sleeping a fixed duration: a fixed sleep only needs to
	// outlast the jitter (up to 1s) plus the 50ms transaction timeout on this
	// machine, at this moment, under this load; require.Eventually asserts
	// the same outcome without hardcoding how long that takes.
	require.Eventually(t, func() bool {
		return mockDB.AcquireRecoveryLeadershipCallCount() >= 2
	}, 5*time.Second, 10*time.Millisecond,
		"leadership must be released and reacquired on the next tick, not held for the hang's duration")

	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop() }()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() deadlocked: the hung Recover call was never bounded")
	}

	require.GreaterOrEqual(t, mockHandler.RecoverCallCount(), 1)

	// Round 7: the claim is no longer released synchronously when the sweep's
	// own attempt times out (see finishAbandonedRecovery) — releasing it then,
	// as pre-round-7 code did, is what let a second replica claim and run a
	// concurrent Recover -> Commit against the same tx while the first
	// replica's abandoned goroutine was still running. This handler honours
	// ctx.Done(), so its goroutine returns shortly after recoverCtx's deadline
	// fires regardless, and finishAbandonedRecovery releases the claim once it
	// does; require.Eventually accounts for that being asynchronous now
	// instead of asserting on it immediately after Stop() returns.
	require.Eventually(t, func() bool {
		return mockDB.ReleaseRecoveryClaimCallCount() >= 1
	}, 5*time.Second, 10*time.Millisecond, "the claim must eventually be released once the abandoned attempt actually returns")

	_, gotTxID, _, gotMsg := mockDB.ReleaseRecoveryClaimArgsForCall(0)
	assert.Equal(t, "txHung", gotTxID)
	assert.Contains(t, gotMsg, "context deadline exceeded")
}

// TestManager_TransactionTimeoutBoundsRecoverIgnoringContext is the sharper
// version of the scenario above: the mock handler here never reads ctx at
// all, simulating a blocking call like FSC's fabric
// Ledger.GetTransactionByID, which takes no context and therefore cannot
// itself be interrupted by context.WithTimeout. If the manager only relied on
// the handler honouring ctx.Done() (as recoverCtx, cancel :=
// context.WithTimeout(...); handler.Recover(recoverCtx, txID) would), this
// test would hang until the real 30s test timeout. It passes only because
// the manager runs Recover in its own goroutine and gives up on
// recoverCtx.Done() independently of whether the handler ever notices.
//
// It also covers the round-7 finding: since the handler here never returns,
// finishAbandonedRecovery never gets a result to release the claim with, so
// ReleaseRecoveryClaim must never be called for the whole test — the claim
// stays held for as long as the abandoned goroutine might still be running,
// instead of being released back for another replica to pick up while a
// Recover call against the same tx is still silently in flight.
// TestManager_TransactionTimeoutBoundsRecoverIgnoringContext also covers the
// round 8 leadership-holding fix (finding 1): while txIgnoresCtx's Recover
// call is abandoned in flight, recovery leadership must be held across
// sweeps, not released and reacquired. Releasing it (round 7's behaviour)
// would let another replica win the advisory lock's non-blocking try-lock on
// a later sweep and reclaim txIgnoresCtx once this instance stopped
// renewing its lease, running Recover -> Commit concurrently with this
// instance's still-silently-running attempt: the exact hazard this test's
// final assertion guards against.
func TestManager_TransactionTimeoutBoundsRecoverIgnoringContext(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                10 * time.Millisecond,
		ScanInterval:       20 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 50 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	txRecord := &ttxdb.RecoveryClaim{TxID: "txIgnoresCtx"}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)

	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	// Never reads ctx: blocks until the test process exits (or GC, in
	// practice never for the duration of this test). The manager must not
	// wait on this to return.
	mockHandler.RecoverStub = func(_ context.Context, _ string) error {
		select {}
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	// Let several sweeps elapse while txIgnoresCtx stays abandoned in flight.
	require.Eventually(t, func() bool {
		return mockDB.ClaimPendingTransactionsCallCount() >= 3
	}, 5*time.Second, 10*time.Millisecond,
		"expected multiple sweeps to run while the claim is held")

	assert.Equal(t, 1, mockDB.AcquireRecoveryLeadershipCallCount(),
		"leadership must be held across sweeps, not released and reacquired, while an abandoned goroutine is still in flight")
	assert.Equal(t, 0, leadership.CloseCallCount(),
		"leadership must not be released while inFlight is non-empty")

	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop() }()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() deadlocked: the manager waited on a handler that ignores ctx")
	}

	assert.Equal(t, 1, leadership.CloseCallCount(),
		"Stop() must always release held leadership, regardless of any still-abandoned goroutine, so it does not block a live peer indefinitely")
	assert.Equal(t, 0, mockDB.ReleaseRecoveryClaimCallCount(),
		"a handler that never returns must never have its claim released: doing so is what let a second replica claim and Commit the same tx while this attempt is still silently running")
}

// TestManager_SkipsReclaimedTransactionStillInFlight is the regression test
// for the round-2 finding: a transaction that keeps timing out stays Pending
// and is re-claimed on every sweep. Before the tryMarkInFlight guard, each
// sweep abandoned another goroutine for the same txID, so a stuck
// transaction accumulated unbounded goroutines and could run duplicate
// concurrent Recover calls (each reaching Commit) for the same tx. The
// handler here never returns, so the very first attempt's goroutine leaks
// forever; every later sweep must still find Recover having been called
// exactly once.
func TestManager_SkipsReclaimedTransactionStillInFlight(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                5 * time.Millisecond,
		ScanInterval:       15 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        1,
		LeaseDuration:      time.Second,
		TransactionTimeout: 20 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	txRecord := &ttxdb.RecoveryClaim{TxID: "txStuck"}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	// Every sweep reclaims the same still-Pending row: it never resolves, so
	// it keeps being the oldest eligible claim.
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	// Never returns: simulates the first sweep's abandoned goroutine still
	// running when later sweeps reclaim the same txID.
	mockHandler.RecoverStub = func(_ context.Context, _ string) error {
		select {}
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	require.Eventually(t, func() bool {
		return mockDB.ClaimPendingTransactionsCallCount() >= 5
	}, 5*time.Second, 10*time.Millisecond, "need several sweeps to reclaim the still-pending row")

	_ = manager.Stop()

	assert.Equal(t, 1, mockHandler.RecoverCallCount(),
		"a transaction whose previous attempt is still in flight must not get a second concurrent Recover call")
}

// sweepLogger records every Warnf/Debugf call verbatim, so
// TestManager_SweepSummary_MixedSkipAndSuccessIsNotReportedAsFullyWedged can
// assert on the exact sweep-summary line logged. Deliberately separate from
// recordingLogger above, which only captures Errorf and is relied on by the
// escalation tests to contain nothing but those calls.
type sweepLogger struct {
	logging.MockLogger
	mu       sync.Mutex
	messages []string
}

func (l *sweepLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

func (l *sweepLogger) Debugf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

func (l *sweepLogger) allMessages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.messages))
	copy(out, l.messages)

	return out
}

// TestManager_SweepSummary_MixedSkipAndSuccessIsNotReportedAsFullyWedged is
// the regression test for the round-4 review finding: recoverTransactions
// reported a sweep as "all skipped, no progress made" as soon as any
// transaction was skipped, even when other transactions in the same sweep
// genuinely succeeded. txStuck never returns, so from the second sweep
// onward it is skipped as still in flight; txOK succeeds on every sweep. A
// sweep claiming both must report the real breakdown, not claim the sweep
// made no progress at all.
func TestManager_SweepSummary_MixedSkipAndSuccessIsNotReportedAsFullyWedged(t *testing.T) {
	logger := &sweepLogger{}
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:            true,
		TTL:                5 * time.Millisecond,
		ScanInterval:       15 * time.Millisecond,
		BatchSize:          100,
		WorkerCount:        2,
		LeaseDuration:      time.Second,
		TransactionTimeout: 20 * time.Millisecond,
		InstanceID:         "test-instance",
	}

	stuckRecord := &ttxdb.RecoveryClaim{TxID: "txStuck"}
	okRecord := &ttxdb.RecoveryClaim{TxID: "txOK"}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{stuckRecord, okRecord}, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	mockHandler.RecoverStub = func(_ context.Context, txID string) error {
		if txID == "txStuck" {
			select {}
		}

		return nil
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	require.Eventually(t, func() bool {
		return mockDB.ClaimPendingTransactionsCallCount() >= 3
	}, 5*time.Second, 10*time.Millisecond, "need several sweeps for txStuck's skip to kick in")

	// Snapshot before Stop(), not after: Stop() can race with a sweep that is
	// still actively dispatching txOK, and round 8 correctly reclassifies an
	// attempt interrupted by that shutdown as skipped rather than failed (see
	// errSkippedShutdown), not as a genuine failure. If that race is lost,
	// the one sweep truncated by Stop() can legitimately have nothing to show
	// for itself and log "all skipped" too, which is not the round-4 bug this
	// test guards against: that bug was a healthy, uninterrupted sweep with a
	// real success being misreported. Reading the log before Stop() looks
	// only at sweeps that ran to completion, which is what round 4 covers.
	msgs := logger.allMessages()
	_ = manager.Stop()
	for _, m := range msgs {
		assert.NotContains(t, m, "all skipped",
			"a sweep with a real success must not be reported as fully wedged: %s", m)
	}

	found := false
	for _, m := range msgs {
		if strings.Contains(m, "skipped=1") && strings.Contains(m, "succeeded=1") {
			found = true

			break
		}
	}
	assert.True(t, found, "expected a sweep-summary line reporting both the skip and the success, got: %v", msgs)
}

// TestManager_StuckTransactionAlertThreshold_EscalatesAndBacksOff verifies
// both that the Error escalation fires once a transaction crosses
// StuckTransactionAlertThreshold, and that it then backs off by doubling
// rather than firing on every sweep (round-2 finding: an unthrottled Error
// log would fire every scanInterval indefinitely, flooding alerting).
func TestManager_StuckTransactionAlertThreshold_EscalatesAndBacksOff(t *testing.T) {
	logger := &recordingLogger{}
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:                        true,
		TTL:                            5 * time.Millisecond,
		ScanInterval:                   15 * time.Millisecond,
		BatchSize:                      100,
		WorkerCount:                    1,
		LeaseDuration:                  time.Second,
		TransactionTimeout:             10 * time.Millisecond,
		InstanceID:                     "test-instance",
		StuckTransactionAlertThreshold: 2,
	}

	txRecord := &ttxdb.RecoveryClaim{TxID: "txStuckAlert"}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	// Cooperative: honours ctx.Done(), so each sweep's attempt genuinely
	// completes (times out) and clears the in-flight guard, letting the next
	// sweep actually re-attempt instead of being skipped by it. This is what
	// lets consecutive timeouts actually accumulate for the escalation.
	mockHandler.RecoverStub = func(ctx context.Context, _ string) error {
		<-ctx.Done()

		return ctx.Err()
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	require.Eventually(t, func() bool {
		return mockHandler.RecoverCallCount() >= 9
	}, 5*time.Second, 10*time.Millisecond, "need several consecutive timeouts to observe the backoff")

	_ = manager.Stop()

	msgs := logger.errorMessages()
	require.NotEmpty(t, msgs, "escalation must fire once the threshold is crossed")
	for _, m := range msgs {
		assert.Contains(t, m, "txStuckAlert")
	}
	// threshold=2 backs off at 2, 4, 8, ...: far fewer crossings than attempts.
	assert.Less(t, len(msgs), mockHandler.RecoverCallCount(),
		"the Error log must back off instead of firing on every sweep")
}

// TestManager_StuckTransactionAlertThreshold_ZeroDisablesEscalation verifies
// the documented opt-out.
func TestManager_StuckTransactionAlertThreshold_ZeroDisablesEscalation(t *testing.T) {
	logger := &recordingLogger{}
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:                        true,
		TTL:                            5 * time.Millisecond,
		ScanInterval:                   15 * time.Millisecond,
		BatchSize:                      100,
		WorkerCount:                    1,
		LeaseDuration:                  time.Second,
		TransactionTimeout:             10 * time.Millisecond,
		InstanceID:                     "test-instance",
		StuckTransactionAlertThreshold: 0,
	}

	txRecord := &ttxdb.RecoveryClaim{TxID: "txStuckNoAlert"}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ReleaseRecoveryClaimReturns(nil)

	mockHandler.RecoverStub = func(ctx context.Context, _ string) error {
		<-ctx.Done()

		return ctx.Err()
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	require.Eventually(t, func() bool {
		return mockHandler.RecoverCallCount() >= 5
	}, 5*time.Second, 10*time.Millisecond, "need several consecutive timeouts")

	_ = manager.Stop()

	assert.Empty(t, logger.errorMessages(), "StuckTransactionAlertThreshold: 0 must disable the escalation entirely")
}

// shutdownLogger records every Warnf/Debugf/Errorf call, so
// TestManager_ShutdownDuringRecoveryIsNotCountedAsTimeout can both check the
// exact message logged and confirm the stuck-transaction escalation (set to
// fire on the very first recordTimeout call here) never fires.
type shutdownLogger struct {
	logging.MockLogger
	mu       sync.Mutex
	messages []string
}

func (l *shutdownLogger) record(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

func (l *shutdownLogger) Warnf(format string, args ...any)  { l.record(format, args...) }
func (l *shutdownLogger) Debugf(format string, args ...any) { l.record(format, args...) }
func (l *shutdownLogger) Errorf(format string, args ...any) { l.record(format, args...) }

func (l *shutdownLogger) allMessages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.messages))
	copy(out, l.messages)

	return out
}

// TestManager_ShutdownDuringRecoveryIsNotCountedAsTimeout is the regression
// test for round 8 review finding 4: recoverCtx.Done() firing because Stop()
// cancelled the parent context looks identical, from callHandler's return
// values alone, to a real TransactionTimeout expiry. Before this fix,
// recoverTransaction could not tell them apart, so a clean shutdown that
// caught an attempt mid-flight was recorded and alerted on exactly like a
// genuinely stuck transaction. TransactionTimeout is set far longer than the
// test needs, and StuckTransactionAlertThreshold to 1, so any recordTimeout
// call at all is enough to prove the bug is present.
func TestManager_ShutdownDuringRecoveryIsNotCountedAsTimeout(t *testing.T) {
	logger := &shutdownLogger{}
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:                        true,
		TTL:                            10 * time.Millisecond,
		ScanInterval:                   100 * time.Millisecond,
		BatchSize:                      100,
		WorkerCount:                    1,
		LeaseDuration:                  time.Minute,
		TransactionTimeout:             time.Minute,
		StuckTransactionAlertThreshold: 1,
		InstanceID:                     "test-instance",
	}

	txRecord := &ttxdb.RecoveryClaim{TxID: "txShutdown"}

	leadership := &mock2.Leadership{}
	leadership.CloseReturns(nil)
	mockDB.AcquireRecoveryLeadershipReturns(leadership, true, nil)
	mockDB.ClaimPendingTransactionsReturnsOnCall(0, []*ttxdb.RecoveryClaim{txRecord}, nil)
	mockDB.ClaimPendingTransactionsReturns([]*ttxdb.RecoveryClaim{}, nil)

	started := make(chan struct{})
	var once sync.Once
	mockHandler.RecoverStub = func(_ context.Context, _ string) error {
		once.Do(func() { close(started) })
		select {} // never returns, regardless of ctx
	}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, config)
	require.NoError(t, manager.Start())

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler was never invoked")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop() }()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() deadlocked")
	}

	for _, msg := range logger.allMessages() {
		assert.NotContains(t, msg, "still running past transactionTimeout",
			"a shutdown-triggered abandonment must not be logged with real-timeout wording: %s", msg)
		assert.NotContains(t, msg, "has timed out",
			"the stuck-transaction escalation must never fire for a shutdown, even at threshold=1: %s", msg)
	}

	found := false
	for _, msg := range logger.allMessages() {
		if strings.Contains(msg, "interrupted") {
			found = true

			break
		}
	}
	assert.True(t, found, "expected a debug log noting the attempt was interrupted rather than timed out, got: %v", logger.allMessages())
}

// TestManager_DefaultConfigDoesNotSelfTriggerLeaseWarnings is the regression
// test for round 8 review finding 5: DefaultConfig previously combined a
// BatchSize/WorkerCount/TransactionTimeout ratio whose worst case always
// exceeded LeaseDuration, so every untouched deployment logged a Warn on
// every single Start().
func TestManager_DefaultConfigDoesNotSelfTriggerLeaseWarnings(t *testing.T) {
	logger := &sweepLogger{}
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}

	manager := recovery2.NewManager(logger, mockDB, mockHandler, recovery2.DefaultConfig())
	require.NoError(t, manager.Start())
	require.NoError(t, manager.Stop())

	for _, msg := range logger.allMessages() {
		assert.NotContains(t, msg, "worst-case sweep duration",
			"the default config must not trip its own mid-sweep lease warning: %s", msg)
		assert.NotContains(t, msg, "scanInterval",
			"the default config must not trip its own between-sweeps lease warning: %s", msg)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := recovery2.DefaultConfig()

	assert.True(t, config.Enabled, "Recovery should be enabled by default")
	assert.Equal(t, 30*time.Second, config.TTL)
	assert.Equal(t, 5*time.Second, config.ScanInterval)
	assert.Equal(t, 16, config.BatchSize)
	assert.Equal(t, 8, config.WorkerCount)
	assert.Equal(t, 5*time.Minute, config.LeaseDuration)
	assert.Equal(t, 60*time.Second, config.TransactionTimeout)
}

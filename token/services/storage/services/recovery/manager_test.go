/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery_test

import (
	"context"
	"errors"
	"strconv"
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

func TestNewManager(t *testing.T) {
	logger := logging.MustGetLogger()
	mockDB := &mock2.Storage{}
	mockHandler := &mock2.Handler{}
	config := recovery2.Config{
		Enabled:        true,
		TTL:            30 * time.Second,
		ScanInterval:   30 * time.Second,
		BatchSize:      100,
		WorkerCount:    1,
		LeaseDuration:  30 * time.Second,
		AdvisoryLockID: 1,
		InstanceID:     "test-instance",
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
		Enabled:        true,
		TTL:            100 * time.Millisecond,
		ScanInterval:   100 * time.Millisecond,
		BatchSize:      100,
		WorkerCount:    1,
		LeaseDuration:  time.Second,
		AdvisoryLockID: 1,
		InstanceID:     "test-instance",
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
		Enabled:        true,
		TTL:            100 * time.Millisecond,
		ScanInterval:   100 * time.Millisecond,
		BatchSize:      100,
		WorkerCount:    1,
		LeaseDuration:  time.Second,
		AdvisoryLockID: 1,
		InstanceID:     "test-instance",
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
		Enabled:        true,
		TTL:            50 * time.Millisecond,
		ScanInterval:   50 * time.Millisecond,
		BatchSize:      100,
		WorkerCount:    1,
		LeaseDuration:  time.Second,
		AdvisoryLockID: 1,
		InstanceID:     "test-instance",
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
		AdvisoryLockID:      1,
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
		AdvisoryLockID:      1,
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
		Enabled:        true,
		TTL:            10 * time.Millisecond,
		ScanInterval:   10 * time.Millisecond,
		BatchSize:      100,
		WorkerCount:    1,
		LeaseDuration:  time.Second,
		AdvisoryLockID: 1,
		InstanceID:     "test-instance",
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

func TestDefaultConfig(t *testing.T) {
	config := recovery2.DefaultConfig()

	assert.True(t, config.Enabled, "Recovery should be enabled by default")
	assert.Equal(t, 30*time.Second, config.TTL)
	assert.Equal(t, 5*time.Second, config.ScanInterval)
	assert.Equal(t, 100, config.BatchSize)
	assert.Equal(t, 4, config.WorkerCount)
	assert.Equal(t, 30*time.Second, config.LeaseDuration)
}

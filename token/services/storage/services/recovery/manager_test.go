/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery_test

import (
	"errors"
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

func TestDefaultConfig(t *testing.T) {
	config := recovery2.DefaultConfig()

	assert.True(t, config.Enabled, "Recovery should be enabled by default")
	assert.Equal(t, 30*time.Second, config.TTL)
	assert.Equal(t, 5*time.Second, config.ScanInterval)
	assert.Equal(t, 100, config.BatchSize)
	assert.Equal(t, 4, config.WorkerCount)
	assert.Equal(t, 30*time.Second, config.LeaseDuration)
}

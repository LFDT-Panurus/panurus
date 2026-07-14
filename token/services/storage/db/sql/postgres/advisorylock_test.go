/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func TestAdvisoryLock_Acquisition(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	db, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db.Close)

	ctx := context.Background()
	lockID := int64(12345)

	// Test: Acquire lock successfully
	lock1, acquired, err := NewAdvisoryLock(ctx, db, lockID)
	require.NoError(t, err)
	require.True(t, acquired, "First lock acquisition should succeed")
	require.NotNil(t, lock1)

	// Test: Second acquisition should fail (lock is held)
	lock2, acquired, err := NewAdvisoryLock(ctx, db, lockID)
	require.NoError(t, err)
	require.False(t, acquired, "Second lock acquisition should fail while first lock is held")
	require.Nil(t, lock2)

	// Test: Release lock
	err = lock1.Close()
	require.NoError(t, err)

	// Test: After release, lock should be acquirable again
	lock3, acquired, err := NewAdvisoryLock(ctx, db, lockID)
	require.NoError(t, err)
	require.True(t, acquired, "Lock acquisition should succeed after release")
	require.NotNil(t, lock3)

	err = lock3.Close()
	require.NoError(t, err)
}

func TestAdvisoryLock_MultipleInstances(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	db1, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db1.Close)

	db2, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db2.Close)

	ctx := context.Background()
	lockID := int64(99999)

	// Instance 1 acquires lock
	lock1, acquired, err := NewAdvisoryLock(ctx, db1, lockID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lock1)

	// Instance 2 tries to acquire same lock (should fail)
	lock2, acquired, err := NewAdvisoryLock(ctx, db2, lockID)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, lock2)

	// Instance 1 releases lock
	err = lock1.Close()
	require.NoError(t, err)

	// Instance 2 can now acquire the lock
	lock3, acquired, err := NewAdvisoryLock(ctx, db2, lockID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lock3)

	err = lock3.Close()
	require.NoError(t, err)
}

func TestAdvisoryLock_DoubleClose(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	db, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db.Close)

	ctx := context.Background()
	lockID := int64(54321)

	lock, acquired, err := NewAdvisoryLock(ctx, db, lockID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lock)

	// First close
	err = lock.Close()
	require.NoError(t, err)

	// Second close should not panic or error
	err = lock.Close()
	require.NoError(t, err)
}

func TestAdvisoryLock_ContextCancellation(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	db, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	lockID := int64(11111)

	// Acquire lock with short-lived context
	lock, acquired, err := NewAdvisoryLock(ctx, db, lockID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lock)

	// Lock should still be valid even after context cancellation
	// (advisory locks are session-scoped, not context-scoped)
	time.Sleep(150 * time.Millisecond)

	// Verify lock is still held by trying to acquire it again
	ctx2 := context.Background()
	lock2, acquired, err := NewAdvisoryLock(ctx2, db, lockID)
	require.NoError(t, err)
	require.False(t, acquired, "Lock should still be held after context cancellation")
	require.Nil(t, lock2)

	// Clean up
	err = lock.Close()
	require.NoError(t, err)
}

func TestAdvisoryLockFactory(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	db, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db.Close)

	ctx := context.Background()
	lockID := int64(77777)

	// Test factory function
	factory := NewAdvisoryLockFactory()
	require.NotNil(t, factory)

	// Use factory to create lock
	lock, acquired, err := factory(ctx, db, lockID)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lock)

	// Verify it's actually locked
	lock2, acquired, err := factory(ctx, db, lockID)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, lock2)

	err = lock.Close()
	require.NoError(t, err)
}

func TestAdvisoryLock_DifferentLockIDs(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	db, err := sql.Open("pgx", pgConnStr)
	require.NoError(t, err)
	defer utils.IgnoreErrorFunc(db.Close)

	ctx := context.Background()
	lockID1 := int64(11111)
	lockID2 := int64(22222)

	// Acquire two different locks
	lock1, acquired, err := NewAdvisoryLock(ctx, db, lockID1)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lock1)

	lock2, acquired, err := NewAdvisoryLock(ctx, db, lockID2)
	require.NoError(t, err)
	require.True(t, acquired, "Different lock IDs should not conflict")
	require.NotNil(t, lock2)

	// Clean up
	err = lock1.Close()
	require.NoError(t, err)
	err = lock2.Close()
	require.NoError(t, err)
}

// TestRecoveryIntegration tests the full recovery flow with advisory locks
func TestRecoveryIntegration(t *testing.T) {
	terminate, pgConnStr := startContainer(t)
	defer terminate()

	// Create driver and transaction store
	driver := NewDriver(postgresCfg(pgConnStr, "recovery_test"))

	ctx := context.Background()

	// Get transaction store (returns interface, but we need concrete type for CreateSchema)
	storeInterface, err := driver.NewOwnerTransaction("test", "recovery_test")
	require.NoError(t, err)
	require.NotNil(t, storeInterface)

	// Cast to concrete type to access CreateSchema
	store, ok := storeInterface.(*TransactionStore)
	require.True(t, ok, "Store should be *TransactionStore")

	// Create schema
	err = store.CreateSchema()
	require.NoError(t, err)

	// Test advisory lock acquisition through the store
	lockID := int64(0x74746b7265636f76) // Default recovery lock ID
	leadership, acquired, err := store.AcquireRecoveryLeadership(ctx, lockID)
	require.NoError(t, err)
	require.True(t, acquired, "Should acquire leadership")
	require.NotNil(t, leadership)

	// Try to acquire again (should fail)
	leadership2, acquired, err := store.AcquireRecoveryLeadership(ctx, lockID)
	require.NoError(t, err)
	require.False(t, acquired, "Should not acquire leadership when already held")
	require.Nil(t, leadership2)

	// Release leadership
	err = leadership.Close()
	require.NoError(t, err)

	// Should be able to acquire again
	leadership3, acquired, err := store.AcquireRecoveryLeadership(ctx, lockID)
	require.NoError(t, err)
	require.True(t, acquired, "Should acquire leadership after release")
	require.NotNil(t, leadership3)

	err = leadership3.Close()
	require.NoError(t, err)
}

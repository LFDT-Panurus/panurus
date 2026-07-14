/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditdb

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore() *StoreService {
	return &StoreService{locker: memory.New()}
}

func TestAcquireLocks_Success(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	err := store.AcquireLocks(ctx, "anchor1", "alice", "bob", "charlie")
	require.NoError(t, err)

	store.ReleaseLocks(ctx, "anchor1")
}

func TestAcquireLocks_Deduplication(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	err := store.AcquireLocks(ctx, "anchor2", "alice", "bob", "alice", "charlie", "bob")
	require.NoError(t, err)

	store.ReleaseLocks(ctx, "anchor2")
}

func TestAcquireLocks_Sorting(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	err := store.AcquireLocks(ctx, "anchor3", "charlie", "alice", "bob")
	require.NoError(t, err)

	store.ReleaseLocks(ctx, "anchor3")
}

func TestAcquireLocks_ContextCancellation(t *testing.T) {
	store := newTestStore()

	ctx1 := context.Background()
	err := store.AcquireLocks(ctx1, "anchor_first", "alice")
	require.NoError(t, err)

	ctx2, cancel := context.WithCancel(context.Background())
	cancel()

	err = store.AcquireLocks(ctx2, "anchor_second", "alice")
	require.Error(t, err, "should fail due to context cancellation")

	store.ReleaseLocks(ctx1, "anchor_first")
}

func TestAcquireLocks_ContextTimeout(t *testing.T) {
	store := newTestStore()

	ctx1 := context.Background()
	err := store.AcquireLocks(ctx1, "anchor_holder", "bob")
	require.NoError(t, err)

	ctx2, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = store.AcquireLocks(ctx2, "anchor_timeout", "bob")
	require.Error(t, err, "should timeout waiting for lock")

	store.ReleaseLocks(ctx1, "anchor_holder")
}

func TestAcquireLocks_PartialAcquisitionRollback(t *testing.T) {
	store := newTestStore()

	ctx1 := context.Background()
	err := store.AcquireLocks(ctx1, "anchor_blocker", "charlie")
	require.NoError(t, err)

	ctx2, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = store.AcquireLocks(ctx2, "anchor_partial", "alice", "bob", "charlie")
	require.Error(t, err, "should fail to acquire all locks")

	ctx3 := context.Background()
	err = store.AcquireLocks(ctx3, "anchor_verify", "alice", "bob")
	require.NoError(t, err, "alice and bob should be available (rollback successful)")

	store.ReleaseLocks(ctx1, "anchor_blocker")
	store.ReleaseLocks(ctx3, "anchor_verify")
}

func TestAcquireLocks_ConcurrentNonOverlapping(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		err := store.AcquireLocks(ctx, "anchor_concurrent1", "alice", "bob")
		assert.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		store.ReleaseLocks(ctx, "anchor_concurrent1")
	}()

	go func() {
		defer wg.Done()
		err := store.AcquireLocks(ctx, "anchor_concurrent2", "charlie", "dave")
		assert.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		store.ReleaseLocks(ctx, "anchor_concurrent2")
	}()

	wg.Wait()
}

func TestAcquireLocks_ConcurrentOverlapping(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			anchor := fmt.Sprintf("anchor_overlap_%d", id)
			err := store.AcquireLocks(ctx, anchor, "shared_resource")
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				store.ReleaseLocks(ctx, anchor)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 5, successCount, "all goroutines should eventually acquire the lock")
}

func TestAcquireLocks_DeadlockPrevention(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		err := store.AcquireLocks(ctx, "anchor_deadlock1", "alice", "bob")
		assert.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		store.ReleaseLocks(ctx, "anchor_deadlock1")
	}()

	go func() {
		defer wg.Done()
		err := store.AcquireLocks(ctx, "anchor_deadlock2", "bob", "alice")
		assert.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
		store.ReleaseLocks(ctx, "anchor_deadlock2")
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected - goroutines did not complete")
	}
}

func TestAcquireLocks_EmptyEnrollmentIDs(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	err := store.AcquireLocks(ctx, "anchor_empty")
	require.NoError(t, err)

	store.ReleaseLocks(ctx, "anchor_empty")
}

func TestAcquireLocks_SingleEnrollmentID(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	err := store.AcquireLocks(ctx, "anchor_single", "alice")
	require.NoError(t, err)

	store.ReleaseLocks(ctx, "anchor_single")
}

func TestReleaseLocks_NonExistentAnchor(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	// Releasing locks for a non-existent anchor should not panic
	store.ReleaseLocks(ctx, "non_existent_anchor")
}

func TestAcquireAndReleaseLocks_Integration(t *testing.T) {
	store := newTestStore()
	ctx := context.Background()

	err := store.AcquireLocks(ctx, "anchor_integration", "alice", "bob", "charlie")
	require.NoError(t, err)

	store.ReleaseLocks(ctx, "anchor_integration")

	// Verify we can acquire the same locks again after release
	err = store.AcquireLocks(ctx, "anchor_integration2", "alice", "bob", "charlie")
	require.NoError(t, err, "should be able to re-acquire released locks")

	store.ReleaseLocks(ctx, "anchor_integration2")
}

func TestStoreService_WithLocker(t *testing.T) {
	custom := &stubLocker{}
	s, err := NewStoreService(nil, WithLocker(custom))
	require.NoError(t, err)
	require.NotNil(t, s)
}

type stubLocker struct{}

func (s *stubLocker) AcquireLocks(context.Context, string, ...string) error { return nil }
func (s *stubLocker) ReleaseLocks(context.Context, string)                  {}
func (s *stubLocker) AssertLocksHeld(context.Context, string) error         { return nil }

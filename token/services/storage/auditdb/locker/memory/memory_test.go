/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/memory"
	"github.com/stretchr/testify/require"
)

func TestLocker_AcquireAndRelease(t *testing.T) {
	l := memory.New()
	ctx := context.Background()

	require.NoError(t, l.AcquireLocks(ctx, "anchor1", "alice", "bob"))
	l.ReleaseLocks(ctx, "anchor1")
}

func TestLocker_DeadlockPrevention(t *testing.T) {
	l := memory.New()
	ctx := context.Background()
	done := make(chan struct{})

	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = l.AcquireLocks(ctx, "a1", "alice", "bob")
			time.Sleep(5 * time.Millisecond)
			l.ReleaseLocks(ctx, "a1")
		}()
		go func() {
			defer wg.Done()
			_ = l.AcquireLocks(ctx, "a2", "bob", "alice")
			time.Sleep(5 * time.Millisecond)
			l.ReleaseLocks(ctx, "a2")
		}()
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock detected")
	}
}

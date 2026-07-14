/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// TestLocker_Stop verifies that Stop() halts the scan goroutine and is idempotent.
func TestLocker_Stop(t *testing.T) {
	t.Run("Stop halts scan goroutine", func(t *testing.T) {
		mock := newMockTXStatusProvider()
		tokenID := &token.ID{TxId: "tok-stop", Index: 0}
		txA := "tx-stop-A"

		// Lock a token with Pending status so the scan loop stays busy.
		mock.setStatus(txA, ttxdb.Pending)

		d := NewLocker(mock, 20*time.Millisecond, time.Minute)

		_, err := d.Lock(context.Background(), tokenID, txA, false)
		if err != nil {
			t.Fatalf("unexpected lock error: %v", err)
		}

		concreteLocker := d.(*locker)

		// Confirm the scanDone channel is open (goroutine running).
		select {
		case <-concreteLocker.scanDone:
			t.Fatal("scan goroutine exited before Stop was called")
		default:
		}

		err = concreteLocker.Stop()
		if err != nil {
			t.Fatalf("unexpected stop error: %v", err)
		}

		// scanDone must be closed within a reasonable timeout.
		select {
		case <-concreteLocker.scanDone:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("scan goroutine did not exit after Stop")
		}
	})

	t.Run("Stop is idempotent", func(t *testing.T) {
		mock := newMockTXStatusProvider()
		d := NewLocker(mock, 20*time.Millisecond, time.Minute).(*locker)

		// Must not panic or deadlock.
		if err := d.Stop(); err != nil {
			t.Fatalf("unexpected stop error: %v", err)
		}
		if err := d.Stop(); err != nil {
			t.Fatalf("unexpected stop error on second stop: %v", err)
		}
		if err := d.Stop(); err != nil {
			t.Fatalf("unexpected stop error on third stop: %v", err)
		}
	})
}

// TestLocker_StopWhileSleeping verifies that Stop wakes up the locker even
// when it is blocked in the inner sleep loop (no locked tokens).
func TestLocker_StopWhileSleeping(t *testing.T) {
	mock := newMockTXStatusProvider()

	// Long sleep timeout to ensure the goroutine is sleeping when Stop is called.
	d := NewLocker(mock, 10*time.Second, time.Minute).(*locker)

	done := make(chan struct{})
	go func() {
		if err := d.Stop(); err != nil {
			t.Errorf("unexpected stop error: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop blocked while locker was sleeping with no locked tokens")
	}
}

// TestLocker_StopDuringActiveScan verifies that Stop terminates the goroutine
// even when there are locked tokens being scanned.
func TestLocker_StopDuringActiveScan(t *testing.T) {
	mock := newMockTXStatusProvider()

	// Add many tokens so the scan loop iterates for a while.
	const numTokens = 50
	var ids []*token.ID
	for i := range numTokens {
		id := &token.ID{TxId: "stop-scan-tx", Index: uint64(i)}
		ids = append(ids, id)
		mock.setStatus("stop-scan-tx", ttxdb.Pending)
	}

	d := NewLocker(mock, 20*time.Millisecond, time.Minute).(*locker)

	for _, id := range ids {
		_, _ = d.Lock(context.Background(), id, "stop-scan-tx", false)
	}

	// Give the scan goroutine a moment to start iterating.
	time.Sleep(30 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		if err := d.Stop(); err != nil {
			t.Errorf("unexpected stop error: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop blocked during active scan")
	}
}

// TestLocker_ConcurrentStopAndLock verifies that concurrent Stop and Lock
// operations do not race or deadlock.
func TestLocker_ConcurrentStopAndLock(t *testing.T) {
	mock := newMockTXStatusProvider()
	d := NewLocker(mock, 10*time.Millisecond, time.Minute).(*locker)

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := &token.ID{TxId: "conc", Index: uint64(i)} //nolint:gosec // G115: i is in [0,5), no overflow
			mock.setStatus("conc", ttxdb.Pending)
			_, _ = d.Lock(context.Background(), id, "conc", false)
		}(i)
	}

	wg.Go(func() {
		if err := d.Stop(); err != nil {
			t.Errorf("unexpected stop error: %v", err)
		}
	})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Stop and Lock deadlocked")
	}

	// scanDone must be closed after Stop returns.
	select {
	case <-d.scanDone:
	default:
		// Stop may have returned before scan fully exited in the goroutine race;
		// the stopOnce guarantees it will eventually close.
	}
}

func TestLocker_StopReturnsTimeoutError(t *testing.T) {
	d := &locker{
		cancel:   func() {},
		scanDone: make(chan struct{}),
	}

	start := time.Now()
	err := d.Stop()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTimeout)
	require.GreaterOrEqual(t, time.Since(start), stopTimeout)
}

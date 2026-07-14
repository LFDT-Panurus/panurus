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

// TestOnError tests the OnError callback
func TestOnError(t *testing.T) {
	ctx := t.Context()

	listener := newTestListener(t, &mock.TransactionDB{})

	// OnError should just log and not panic
	listener.OnError(ctx, "test-tx-id", errors.New("test error"))
}

// TestCheckTokenRequest tests the hash comparison logic used by checkTokenRequest
func TestCheckTokenRequest(t *testing.T) {
	t.Run("matching hashes", func(t *testing.T) {
		data := []byte("test data")
		hash := utils.Hashable(data).String()
		reference, err := base64.StdEncoding.DecodeString(hash)
		require.NoError(t, err)

		// Verify hash calculation works correctly
		assert.NotEmpty(t, hash)
		assert.NotEmpty(t, reference)

		// Verify the hash can be decoded and re-encoded
		reencoded := base64.StdEncoding.EncodeToString(reference)
		assert.Equal(t, hash, reencoded)
	})

	t.Run("non-matching hashes", func(t *testing.T) {
		data1 := []byte("test data 1")
		data2 := []byte("test data 2")
		hash1 := utils.Hashable(data1).String()
		hash2 := utils.Hashable(data2).String()

		// Verify hashes are different
		assert.NotEqual(t, hash1, hash2)
	})
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery

import (
	"context"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStorage is a hand-written Storage double, not the counterfeiter mock in
// this package's mock subpackage: that mock imports this package to reference
// its interfaces, so an internal (package recovery) test file cannot import
// it without an import cycle. External tests use the counterfeiter mock; this
// file needs the internal-test package for direct access to unexported
// Manager fields and methods.
type stubStorage struct {
	claims      []*ttxdb.RecoveryClaim
	releaseFn   func(ctx context.Context, txID, owner, message string) error
	setStatusFn func(ctx context.Context, txID string, status storage.TxStatus, message string) error
}

func (s *stubStorage) AcquireRecoveryLeadership(context.Context) (Leadership, bool, error) {
	return nil, false, nil
}

func (s *stubStorage) ClaimPendingTransactions(context.Context, time.Duration, time.Duration, int, string) ([]*ttxdb.RecoveryClaim, error) {
	return s.claims, nil
}

func (s *stubStorage) ReleaseRecoveryClaim(ctx context.Context, txID, owner, message string) error {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, txID, owner, message)
	}

	return nil
}

func (s *stubStorage) SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error {
	if s.setStatusFn != nil {
		return s.setStatusFn(ctx, txID, status, message)
	}

	return nil
}

// TestFinishAbandonedRecovery_ClearsInFlightOnlyAfterReleaseCompletes is the
// regression test for round 8 review finding 2: clearing inFlight before the
// release completes lets a sweep that reclaims txID in that window start a
// second, concurrent Recover call, which the finisher's later release then
// pulls the rug out from under.
func TestFinishAbandonedRecovery_ClearsInFlightOnlyAfterReleaseCompletes(t *testing.T) {
	releaseStarted := make(chan struct{})
	releaseProceed := make(chan struct{})
	store := &stubStorage{
		releaseFn: func(context.Context, string, string, string) error {
			close(releaseStarted)
			<-releaseProceed

			return nil
		},
	}

	m := NewManager(logging.MustGetLogger(), store, nil, Config{InstanceID: "inst"})
	//nolint:fatcontext // test double for Start's own field assignment, not a per-request context
	m.ctx, m.cancel = context.WithCancel(context.Background())
	defer m.cancel()

	const txID = "tx1"
	require.True(t, m.tryMarkInFlight(txID))

	resultCh := make(chan error, 1)
	resultCh <- nil
	_, cancelRecover := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.finishAbandonedRecovery(m.ctx, "inst", txID, time.Time{}, resultCh, cancelRecover)
		close(done)
	}()

	select {
	case <-releaseStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("release was never started")
	}

	assert.False(t, m.tryMarkInFlight(txID),
		"inFlight must still be held while the release is in progress, or a concurrent sweep could start a second Recover call for the same tx")

	close(releaseProceed)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("finishAbandonedRecovery never returned")
	}

	assert.True(t, m.tryMarkInFlight(txID), "inFlight must be cleared once the release has actually completed")
}

// TestFinishAbandonedRecovery_SkipsOrphanPromotionAfterStop is the regression
// test for round 8 review finding 3: SetStatus is unconditional and
// owner-blind, unlike the owner-scoped release beside it, so once Stop() has
// released leadership a live peer could have already resolved txID (e.g. to
// Confirmed) by the time this detached finisher's NotFound result arrives.
// Promoting to Orphan in that case would stomp a real outcome with a stale
// one; finishAbandonedRecovery must skip the promotion once it observes the
// manager already stopped.
func TestFinishAbandonedRecovery_SkipsOrphanPromotionAfterStop(t *testing.T) {
	setStatusCalls := 0
	store := &stubStorage{
		setStatusFn: func(context.Context, string, storage.TxStatus, string) error {
			setStatusCalls++

			return nil
		},
	}

	m := NewManager(logging.MustGetLogger(), store, nil, Config{
		InstanceID:          "inst",
		NotFoundGracePeriod: time.Millisecond,
	})
	//nolint:fatcontext // test double for Start's own field assignment, not a per-request context
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.cancel() // simulate Stop() having already run before this attempt returns

	const txID = "tx1"
	m.tryMarkInFlight(txID)

	resultCh := make(chan error, 1)
	resultCh <- errors.New("rpc error: code = NotFound desc = transaction ID [tx1]: not found in index: tx not found")
	_, cancelRecover := context.WithCancel(context.Background())

	storedAt := time.Now().Add(-time.Hour) // well past the 1ms grace period

	m.finishAbandonedRecovery(m.ctx, "inst", txID, storedAt, resultCh, cancelRecover)

	assert.Equal(t, 0, setStatusCalls,
		"must not promote to Orphan once Stop() has already run: a live peer may have since resolved txID and SetStatus has no owner/status guard to protect that outcome")
}

// TestPreferResult exercises the tie-break directly, without relying on real
// goroutine/timer timing to force the race between a handler result and a
// deadline firing at the same instant.
func TestPreferResult(t *testing.T) {
	t.Run("returns the result and ok=true when it arrived before the deadline fired", func(t *testing.T) {
		resultCh := make(chan error, 1)
		resultCh <- nil

		ok, err := preferResult(resultCh)
		assert.True(t, ok)
		assert.NoError(t, err)
	})

	t.Run("reports ok=false when the handler has not finished yet", func(t *testing.T) {
		resultCh := make(chan error, 1)

		ok, _ := preferResult(resultCh)
		assert.False(t, ok)
	})
}

func TestIsPowerOfTwo(t *testing.T) {
	assert.False(t, isPowerOfTwo(0))
	assert.False(t, isPowerOfTwo(-2))
	assert.True(t, isPowerOfTwo(1))
	assert.True(t, isPowerOfTwo(2))
	assert.False(t, isPowerOfTwo(3))
	assert.True(t, isPowerOfTwo(4))
	assert.False(t, isPowerOfTwo(5))
	assert.True(t, isPowerOfTwo(8))
}

// handlerFunc adapts a function to Handler.
type handlerFunc func(ctx context.Context, txID string) error

func (f handlerFunc) Recover(ctx context.Context, txID string) error { return f(ctx, txID) }

// TestRecoverTransactions_AllSkippedIsNoProgress covers #2317: a sweep whose every claimed
// transaction is skipped, because the previous attempt for it never returned, used to return
// nil and read as a clean sweep to anything driven off the result. It now returns
// ErrNoProgress, and a sweep that recovers anything at all still does not.
func TestRecoverTransactions_AllSkippedIsNoProgress(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	handler := handlerFunc(func(ctx context.Context, txID string) error {
		if txID == "stuck" {
			<-release // ignores ctx, like a ledger call that never honours it
		}

		return nil
	})
	store := &stubStorage{claims: []*ttxdb.RecoveryClaim{{TxID: "stuck"}}}
	m := NewManager(logging.MustGetLogger(), store, handler, Config{
		InstanceID: "inst", BatchSize: 10, WorkerCount: 2, TransactionTimeout: 20 * time.Millisecond,
	})

	// The first sweep abandons the attempt when its deadline fires: a timeout, not a skip.
	err := m.recoverTransactions(t.Context())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoProgress)

	// From then on every sweep skips it.
	err = m.recoverTransactions(t.Context())
	require.ErrorIs(t, err, ErrNoProgress)

	// A sweep that also recovers something made progress, skip or not.
	store.claims = []*ttxdb.RecoveryClaim{{TxID: "stuck"}, {TxID: "ok"}}
	require.NoError(t, m.recoverTransactions(t.Context()))
}

// TestStuckTransactions lists the attempts running past TransactionTimeout, oldest first,
// and leaves out the ones still within it.
func TestStuckTransactions(t *testing.T) {
	m := NewManager(logging.MustGetLogger(), &stubStorage{}, nil, Config{InstanceID: "inst", TransactionTimeout: time.Minute})
	now := time.Now()
	m.inFlight["recent"] = now.Add(-10 * time.Second)
	m.inFlight["older"] = now.Add(-2 * time.Hour)
	m.inFlight["old"] = now.Add(-time.Hour)

	stuck := m.StuckTransactions()
	require.Len(t, stuck, 2)
	assert.Equal(t, "older", stuck[0].TxID)
	assert.Equal(t, "old", stuck[1].TxID)
	assert.Equal(t, now.Add(-time.Hour), stuck[1].Since)
	assert.Equal(t, "old (for 1h0m0s)", stuck[1].String())

	unbounded := NewManager(logging.MustGetLogger(), &stubStorage{}, nil, Config{InstanceID: "inst"})
	unbounded.inFlight["old"] = now.Add(-time.Hour)
	assert.Empty(t, unbounded.StuckTransactions(), "without a TransactionTimeout no attempt is ever abandoned")
}

// TestStuckTransactions_TracksAnAbandonedAttempt checks the snapshot follows a real
// abandoned attempt: listed while it runs, gone once it returns.
func TestStuckTransactions_TracksAnAbandonedAttempt(t *testing.T) {
	release := make(chan struct{})
	handler := handlerFunc(func(context.Context, string) error {
		<-release

		return nil
	})
	m := NewManager(logging.MustGetLogger(), &stubStorage{}, handler, Config{InstanceID: "inst", TransactionTimeout: 20 * time.Millisecond})

	finished, err := m.callHandler(t.Context(), "inst", "tx1", time.Time{})
	require.False(t, finished)
	require.Error(t, err)

	require.Eventually(t, func() bool { return len(m.StuckTransactions()) == 1 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, "tx1", m.StuckTransactions()[0].TxID)

	close(release)
	require.Eventually(t, func() bool { return len(m.StuckTransactions()) == 0 }, 2*time.Second, 5*time.Millisecond,
		"an attempt that returned is no longer stuck")
}

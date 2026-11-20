/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package recovery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
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
	releaseFn    func(ctx context.Context, txID, owner, message string) error
	renewFn      func(ctx context.Context, txID, owner string, lease time.Duration) (bool, error)
	markOrphanFn func(ctx context.Context, txID, owner, message string) (bool, error)
}

func (s *stubStorage) AcquireRecoveryLeadership(context.Context) (Leadership, bool, error) {
	return nil, false, nil
}

func (s *stubStorage) ClaimPendingTransactions(context.Context, time.Duration, time.Duration, int, string) ([]*ttxdb.RecoveryClaim, error) {
	return nil, nil
}

func (s *stubStorage) ReleaseRecoveryClaim(ctx context.Context, txID, owner, message string) error {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, txID, owner, message)
	}

	return nil
}

func (s *stubStorage) RenewRecoveryClaim(ctx context.Context, txID, owner string, lease time.Duration) (bool, error) {
	if s.renewFn != nil {
		return s.renewFn(ctx, txID, owner, lease)
	}

	return true, nil
}

func (s *stubStorage) MarkRecoveryOrphan(ctx context.Context, txID, owner, message string) (bool, error) {
	if s.markOrphanFn != nil {
		return s.markOrphanFn(ctx, txID, owner, message)
	}

	return true, nil
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
		m.finishAbandonedRecovery("inst", txID, time.Time{}, resultCh, cancelRecover, func() {})
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

// TestFinishAbandonedRecovery_PromotesOrphanOnlyThroughTheClaim covers #2322: an attempt
// that finishes after Stop() released leadership used to skip the Orphan promotion
// altogether, since an unconditional SetStatus could stomp an outcome a peer wrote after
// taking the claim over. The promotion now goes through MarkRecoveryOrphan, which only
// writes while the transaction is still Pending and still claimed by this instance, so it
// is attempted, scoped to the claim's owner, and its verdict decides how the claim is
// released.
func TestFinishAbandonedRecovery_PromotesOrphanOnlyThroughTheClaim(t *testing.T) {
	notFound := errors.New("rpc error: code = NotFound desc = transaction ID [tx1]: not found in index: tx not found")
	storedAt := time.Now().Add(-time.Hour) // well past the 1ms grace period

	for _, tc := range []struct {
		name        string
		marked      bool
		wantRelease string
	}{
		{name: "still ours: promoted", marked: true, wantRelease: "tx marked Orphan after NotFound grace period"},
		{name: "taken over or resolved meanwhile: left alone", marked: false, wantRelease: "recovery failed: " + notFound.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var markOwner, releaseMessage string
			store := &stubStorage{
				markOrphanFn: func(_ context.Context, _ string, owner, _ string) (bool, error) {
					markOwner = owner

					return tc.marked, nil
				},
				releaseFn: func(_ context.Context, _ string, _ string, message string) error {
					releaseMessage = message

					return nil
				},
			}
			m := NewManager(logging.MustGetLogger(), store, nil, Config{InstanceID: "inst", NotFoundGracePeriod: time.Millisecond})
			//nolint:fatcontext // test double for Start's own field assignment, not a per-request context
			m.ctx, m.cancel = context.WithCancel(context.Background())
			m.cancel() // Stop() already ran before this attempt returns

			const txID = "tx1"
			m.tryMarkInFlight(txID)
			resultCh := make(chan error, 1)
			resultCh <- notFound
			_, cancelRecover := context.WithCancel(context.Background())

			m.finishAbandonedRecovery("inst-at-claim-time", txID, storedAt, resultCh, cancelRecover, func() {})

			assert.Equal(t, "inst-at-claim-time", markOwner, "the promotion must be scoped to the owner that made the claim")
			assert.Equal(t, tc.wantRelease, releaseMessage)
			assert.True(t, m.tryMarkInFlight(txID), "inFlight must be cleared once the attempt is finalized")
		})
	}
}

// TestFinishAbandonedRecovery_StopsRenewalBeforeReleasing checks the claim renewal for an
// abandoned attempt is stopped before its claim is released: a renewal racing the release
// would otherwise be pointless at best.
func TestFinishAbandonedRecovery_StopsRenewalBeforeReleasing(t *testing.T) {
	var events []string
	store := &stubStorage{
		releaseFn: func(context.Context, string, string, string) error {
			events = append(events, "release")

			return nil
		},
	}
	m := NewManager(logging.MustGetLogger(), store, nil, Config{InstanceID: "inst"})

	resultCh := make(chan error, 1)
	resultCh <- nil
	_, cancelRecover := context.WithCancel(context.Background())
	m.finishAbandonedRecovery("inst", "tx1", time.Time{}, resultCh, cancelRecover, func() { events = append(events, "stop renewal") })

	assert.Equal(t, []string{"stop renewal", "release"}, events)
}

// renewRecorder records RenewRecoveryClaim calls and answers them from a script.
type renewRecorder struct {
	mu      sync.Mutex
	calls   int
	answers []renewAnswer
	called  chan struct{}
}

type renewAnswer struct {
	renewed bool
	err     error
}

func newRenewRecorder(answers ...renewAnswer) *renewRecorder {
	return &renewRecorder{answers: answers, called: make(chan struct{}, 100)}
}

func (r *renewRecorder) renew(_ context.Context, txID, owner string, lease time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	answer := renewAnswer{renewed: true}
	if r.calls < len(r.answers) {
		answer = r.answers[r.calls]
	}
	r.calls++
	r.called <- struct{}{}

	return answer.renewed, answer.err
}

func (r *renewRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.calls
}

func (r *renewRecorder) waitCalls(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-r.called:
		case <-time.After(5 * time.Second):
			t.Fatalf("expected %d renewal(s), got %d", n, r.count())
		}
	}
}

// TestRenewClaimUntilStopped_RenewsUntilStopped is the other half of #2322: the lease of an
// abandoned attempt is renewed directly, right away and then every third of the lease, for
// as long as the attempt runs, without any sweep or leadership involved.
func TestRenewClaimUntilStopped_RenewsUntilStopped(t *testing.T) {
	recorder := newRenewRecorder()
	var gotOwner, gotTx string
	var gotLease time.Duration
	store := &stubStorage{renewFn: func(ctx context.Context, txID, owner string, lease time.Duration) (bool, error) {
		gotOwner, gotTx, gotLease = owner, txID, lease

		return recorder.renew(ctx, txID, owner, lease)
	}}
	m := NewManager(logging.MustGetLogger(), store, nil, Config{InstanceID: "inst"})

	stop := m.renewClaimUntilStopped("owner-1", "tx1", 30*time.Millisecond)
	recorder.waitCalls(t, 3)
	stop()

	assert.Equal(t, "owner-1", gotOwner)
	assert.Equal(t, "tx1", gotTx)
	assert.Equal(t, 30*time.Millisecond, gotLease)
	after := recorder.count()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, after, recorder.count(), "no renewal may happen once stopped")
}

// TestRenewClaimUntilStopped_GivesUpOnceTheClaimIsGone checks renewal stops on its own once
// the store reports the claim is no longer this instance's, and keeps going through errors.
func TestRenewClaimUntilStopped_GivesUpOnceTheClaimIsGone(t *testing.T) {
	recorder := newRenewRecorder(
		renewAnswer{renewed: true},
		renewAnswer{err: errors.New("connection reset")},
		renewAnswer{renewed: false},
	)
	m := NewManager(logging.MustGetLogger(), &stubStorage{renewFn: recorder.renew}, nil, Config{InstanceID: "inst"})

	stop := m.renewClaimUntilStopped("owner-1", "tx1", 30*time.Millisecond)
	recorder.waitCalls(t, 3)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 3, recorder.count(), "renewal must stop once the claim is gone, after retrying through the error")
	stop() // still safe to call after the renewal ended by itself
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

// TestRenewClaimUntilStopped_RenewsRightAway checks the first renewal does not wait a whole
// interval: by the time an attempt is abandoned it has already used up TransactionTimeout of
// the lease it was claimed with, which can be most of it.
func TestRenewClaimUntilStopped_RenewsRightAway(t *testing.T) {
	recorder := newRenewRecorder()
	m := NewManager(logging.MustGetLogger(), &stubStorage{renewFn: recorder.renew}, nil, Config{InstanceID: "inst"})

	stop := m.renewClaimUntilStopped("owner-1", "tx1", time.Minute) // first tick would be 20s away
	defer stop()
	select {
	case <-recorder.called:
	case <-time.After(2 * time.Second):
		t.Fatal("the claim was not renewed right away")
	}
}

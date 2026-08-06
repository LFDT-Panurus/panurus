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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockEntry(t *testing.T) {
	m := map[token.ID]string{}

	id1 := token.ID{
		TxId:  "a",
		Index: 0,
	}
	id2 := token.ID{
		TxId:  "a",
		Index: 0,
	}

	m[id1] = "a"
	m[id2] = "b"
	assert.Len(t, m, 1)
	assert.Equal(t, "b", m[id1])
	assert.Equal(t, "b", m[id2])
}

// mockTXStatusProvider is a thread-safe mock that allows tests to control
// the status returned for each txID and to inject hooks.
type mockTXStatusProvider struct {
	mu       sync.Mutex
	statuses map[string]ttxdb.TxStatus
	// getStatusHook, if set, is called at the beginning of every GetStatus.
	// Tests use it to synchronize with (or block) status lookups.
	// Guarded by mu: it may be armed while a locker's scan goroutine is
	// already calling GetStatus, so it must never be assigned directly.
	getStatusHook func(txID string)
}

func newMockTXStatusProvider() *mockTXStatusProvider {
	return &mockTXStatusProvider{statuses: make(map[string]ttxdb.TxStatus)}
}

// setGetStatusHook installs hook, to be called at the beginning of every
// subsequent GetStatus. It is safe to call while status lookups are in flight.
func (m *mockTXStatusProvider) setGetStatusHook(hook func(txID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getStatusHook = hook
}

func (m *mockTXStatusProvider) setStatus(txID string, status ttxdb.TxStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[txID] = status
}

func (m *mockTXStatusProvider) status(txID string) ttxdb.TxStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[txID]
	if !ok {
		return ttxdb.Pending
	}

	return status
}

func (m *mockTXStatusProvider) GetStatus(_ context.Context, txID string) (ttxdb.TxStatus, string, error) {
	m.mu.Lock()
	hook := m.getStatusHook
	m.mu.Unlock()
	if hook != nil {
		hook(txID)
	}

	return m.status(txID), "", nil
}

// Bounds for the coordination in TestScannerDoesNotDeleteReclaimed. They are far longer than the
// 20ms scan interval the test drives, so they only fire when something is genuinely wrong, but they
// keep a missed interleaving from turning into a 10 minute package timeout. Both stay under
// stopTimeout so that a failing run still lets t.Cleanup's Stop join the scan goroutine instead of
// leaving it parked in the hook and running its delete phase during later tests. See #2156.
const (
	scannerObserveTimeout = 3 * time.Second
	hookReleaseTimeout    = 3 * time.Second
)

// TestScannerDoesNotDeleteReclaimed verifies the TOCTOU protection in the
// scanner: when the scanner has observed a token as removable (its tx is
// Deleted) and a concurrent Lock(reclaim=true) re-locks that token for a new
// transaction before the scanner deletes, the scanner must NOT delete the
// new entry. The test drives the real scan loop and blocks the scanner's
// status lookup to open the race window deterministically.
func TestScannerDoesNotDeleteReclaimed(t *testing.T) {
	mock := newMockTXStatusProvider()
	tokenID := &token.ID{TxId: "tok1", Index: 0}
	txA := "tx-A"
	txB := "tx-B"

	// Lock the token for tx-A while it is still Pending.
	mock.setStatus(txA, ttxdb.Pending)
	d := NewLocker(mock, 20*time.Millisecond, time.Minute).(*locker)
	t.Cleanup(func() { _ = d.Stop() })
	_, err := d.Lock(context.Background(), "w1", tokenID, txA, false)
	require.NoError(t, err)

	// Arm a one-shot hook: the next status lookup that observes tx-A as
	// Deleted blocks — that is the scanner mid-evaluation, before its
	// delete phase.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mock.setGetStatusHook(func(txID string) {
		if txID == txA && mock.status(txA) == ttxdb.Deleted {
			first := false
			once.Do(func() { first = true })
			if !first {
				return
			}
			// only the first observer (the scanner) blocks; later lookups
			// (the reclaim's) must pass through or they would deadlock
			close(entered)
			// Bounded: if the test fails before reaching close(release), this
			// goroutine must not sit here forever. This blocks the collector's
			// lookup phase, which deliberately runs without the shard lock, so
			// parking here does not wedge the shard.
			select {
			case <-release:
			case <-time.After(hookReleaseTimeout):
			}
		}
	})
	mock.setStatus(txA, ttxdb.Deleted)

	// The scanner is now stuck between observing tx-A as removable and
	// deleting it. Reclaim the token for tx-B in that window.
	//
	// Bounded rather than a bare receive: this test has hung in CI until the
	// 10 minute package timeout, which hides the failure and takes the rest of
	// the package's results with it. Failing here says which wait did not
	// complete. See #2156.
	select {
	case <-entered:
	case <-time.After(scannerObserveTimeout):
		t.Fatal("scanner never observed tx-A as Deleted: the status hook did not fire within " +
			scannerObserveTimeout.String())
	}
	mock.setStatus(txB, ttxdb.Pending)
	_, err = d.Lock(context.Background(), "w1", tokenID, txB, true)
	require.NoError(t, err)

	// Let the scanner finish its delete phase.
	close(release)

	// The token must remain locked by tx-B: the scanner's re-validation
	// must notice the entry changed hands.
	require.Never(t, func() bool {
		return !d.IsLocked(tokenID)
	}, 300*time.Millisecond, 10*time.Millisecond, "scanner must not delete a reclaimed entry")
	holder, err := d.Lock(context.Background(), "w1", tokenID, "tx-C", false)
	require.ErrorIs(t, err, AlreadyLockedError)
	assert.Equal(t, txB, holder, "token must remain locked by tx-B")
}

// TestScannerDeletesStaleEntry verifies that the scanner still correctly
// removes entries that have NOT been reclaimed (the normal path).
func TestScannerDeletesStaleEntry(t *testing.T) {
	mock := newMockTXStatusProvider()
	tokenID := &token.ID{TxId: "tok2", Index: 0}
	txA := "tx-A"

	mock.setStatus(txA, ttxdb.Pending)
	d := NewLocker(mock, 20*time.Millisecond, time.Minute).(*locker)
	t.Cleanup(func() { _ = d.Stop() })

	_, err := d.Lock(context.Background(), "w1", tokenID, txA, false)
	require.NoError(t, err)
	require.True(t, d.IsLocked(tokenID))

	// Once the transaction is Deleted, the scanner must evict the lock.
	mock.setStatus(txA, ttxdb.Deleted)
	require.Eventually(t, func() bool {
		return !d.IsLocked(tokenID)
	}, 2*time.Second, 20*time.Millisecond, "stale entry should have been removed by scanner")
}

// TestReclaimDoesNotHoldShardLockDuringStatusLookup pins the invariant the collector
// already documents: a status lookup must never run while the shard lock is held.
// Lock(reclaim=true) used to call GetStatus from under the write lock, so a slow or
// stuck transaction store blocked every other operation on that owner's shard until
// it returned.
func TestReclaimDoesNotHoldShardLockDuringStatusLookup(t *testing.T) {
	mock := newMockTXStatusProvider()
	tokenID := &token.ID{TxId: "tok1", Index: 0}
	txA := "tx-A"

	mock.setStatus(txA, ttxdb.Pending)
	d := NewLocker(mock, time.Hour, time.Hour).(*locker)
	t.Cleanup(func() { _ = d.Stop() })
	_, err := d.Lock(context.Background(), "w1", tokenID, txA, false)
	require.NoError(t, err)

	// Stop the collector before arming the hook. A long sleep timeout is not enough
	// to keep it away: scan runs a full pass before its first sleep, so it can reach
	// GetStatus while the hook is armed, consume the one-shot below and park there
	// itself. The reclaim's own lookup would then pass straight through and the test
	// would pass without ever holding a lookup open inside Lock, which it does even
	// on the unfixed code. This test is about the locking path, so take the collector
	// out of the picture entirely.
	require.NoError(t, d.Stop())

	// Block the next status lookup for tx-A, standing in for a slow transaction store.
	inLookup := make(chan struct{})
	unblock := make(chan struct{})
	var once sync.Once
	mock.setGetStatusHook(func(txID string) {
		// Only the reclaim looks up tx-A now that the collector is stopped, so the
		// one-shot is belt and braces rather than load bearing.
		if txID != txA {
			return
		}
		first := false
		once.Do(func() { first = true })
		if !first {
			return
		}
		close(inLookup)
		<-unblock
	})

	// Reclaim in the background; it will stall inside the status lookup.
	reclaimReturned := make(chan struct{})
	go func() {
		defer close(reclaimReturned)
		_, _ = d.Lock(context.Background(), "w1", tokenID, "tx-B", true)
	}()

	select {
	case <-inLookup:
	case <-time.After(5 * time.Second):
		close(unblock)
		t.Fatal("reclaim never reached the status lookup")
	}

	// The shard must still be usable while that lookup is outstanding. Before the
	// fix this blocked on the write lock the reclaim was holding, and the test
	// timed out here.
	done := make(chan bool, 1)
	go func() { done <- d.IsLocked(tokenID) }()

	select {
	case locked := <-done:
		assert.True(t, locked, "token should still be reported as locked")
	case <-time.After(5 * time.Second):
		close(unblock)
		t.Fatal("IsLocked blocked while a reclaim's status lookup was in flight: " +
			"the shard lock is being held across GetStatus")
	}

	close(unblock)
	<-reclaimReturned
}

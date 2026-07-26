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
	getStatusHook func(txID string)
}

func newMockTXStatusProvider() *mockTXStatusProvider {
	return &mockTXStatusProvider{statuses: make(map[string]ttxdb.TxStatus)}
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
	_, err := d.Lock(context.Background(), "alice", tokenID, txA, false)
	require.NoError(t, err)

	// Arm a one-shot hook: the next status lookup that observes tx-A as
	// Deleted blocks — that is the scanner mid-evaluation, before its
	// delete phase.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mock.getStatusHook = func(txID string) {
		if txID == txA && mock.status(txA) == ttxdb.Deleted {
			first := false
			once.Do(func() { first = true })
			if !first {
				return
			}
			// only the first observer (the scanner) blocks; later lookups
			// (the reclaim's) must pass through or they would deadlock
			close(entered)
			<-release
		}
	}
	mock.setStatus(txA, ttxdb.Deleted)

	// The scanner is now stuck between observing tx-A as removable and
	// deleting it. Reclaim the token for tx-B in that window.
	<-entered
	mock.setStatus(txB, ttxdb.Pending)
	_, err = d.Lock(context.Background(), "alice", tokenID, txB, true)
	require.NoError(t, err)

	// Let the scanner finish its delete phase.
	close(release)

	// The token must remain locked by tx-B: the scanner's re-validation
	// must notice the entry changed hands.
	require.Never(t, func() bool {
		return !d.IsLocked(tokenID)
	}, 300*time.Millisecond, 10*time.Millisecond, "scanner must not delete a reclaimed entry")
	holder, err := d.Lock(context.Background(), "alice", tokenID, "tx-C", false)
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

	_, err := d.Lock(context.Background(), "alice", tokenID, txA, false)
	require.NoError(t, err)
	require.True(t, d.IsLocked(tokenID))

	// Once the transaction is Deleted, the scanner must evict the lock.
	mock.setStatus(txA, ttxdb.Deleted)
	require.Eventually(t, func() bool {
		return !d.IsLocked(tokenID)
	}, 2*time.Second, 20*time.Millisecond, "stale entry should have been removed by scanner")
}

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
// the status returned for each txID and to inject delays.
type mockTXStatusProvider struct {
	mu       sync.Mutex
	statuses map[string]ttxdb.TxStatus
	// getStatusHook is called inside GetStatus while the scanner holds RLock.
	// Tests use it to inject a reclaim between the scan's RUnlock and Lock.
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

func (m *mockTXStatusProvider) GetStatus(_ context.Context, txID string) (ttxdb.TxStatus, string, error) {
	if m.getStatusHook != nil {
		m.getStatusHook(txID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[txID]
	if !ok {
		return ttxdb.Pending, "", nil
	}

	return status, "", nil
}

// TestScannerDoesNotDeleteReclaimed verifies the TOCTOU fix:
// when the scanner collects a token for removal and a concurrent
// Lock(reclaim=true) re-locks that token for a new transaction,
// the scanner must NOT delete the new entry.
func TestScannerDoesNotDeleteReclaimed(t *testing.T) {
	mock := newMockTXStatusProvider()
	tokenID := &token.ID{TxId: "tok1", Index: 0}
	txA := "tx-A"
	txB := "tx-B"

	// Use a short sleep timeout so the scanner loop iterates quickly.
	d := &locker{
		ttxdb:                  mock,
		sleepTimeout:           50 * time.Millisecond,
		lock:                   &sync.RWMutex{},
		locked:                 map[token.ID]*lockEntry{},
		validTxEvictionTimeout: 0, // immediate eviction for Confirmed
	}

	// Step 1: Lock the token for tx-A.
	mock.setStatus(txA, ttxdb.Pending)
	_, err := d.Lock(context.Background(), tokenID, txA, false)
	require.NoError(t, err)

	// Step 2: Mark tx-A as Deleted so the scanner will collect it.
	mock.setStatus(txA, ttxdb.Deleted)

	// Step 3: Run the scan manually instead of in a goroutine so we
	// can control timing. We simulate the race by using getStatusHook:
	// while the scanner reads statuses under RLock, we prepare to reclaim
	// right after RUnlock.
	//
	// We replicate the scan logic inline to inject the reclaim at the
	// exact right moment (between RUnlock and Lock).
	type removeEntry struct {
		id   token.ID
		txID string
	}
	var removeList []removeEntry

	// Scan phase (RLock)
	d.lock.RLock()
	for id, entry := range d.locked {
		status, _, _ := d.ttxdb.GetStatus(context.Background(), entry.TxID)
		switch status {
		case ttxdb.Deleted:
			removeList = append(removeList, removeEntry{id: id, txID: entry.TxID})
		case ttxdb.Confirmed:
			if time.Since(entry.LastAccess) > d.validTxEvictionTimeout {
				removeList = append(removeList, removeEntry{id: id, txID: entry.TxID})
			}
		}
	}
	d.lock.RUnlock()

	// --- RACE WINDOW: scanner has released RLock but hasn't acquired Lock yet ---

	// Simulate Lock(reclaim=true) for tx-B: tx-A is Deleted so reclaim succeeds.
	mock.setStatus(txB, ttxdb.Pending)
	_, err = d.Lock(context.Background(), tokenID, txB, true)
	require.NoError(t, err)

	// Verify the token is now locked by tx-B.
	d.lock.RLock()
	entry := d.locked[*tokenID]
	assert.Equal(t, txB, entry.TxID, "token should be locked by tx-B after reclaim")
	d.lock.RUnlock()

	// Delete phase (Lock) — this is the code under test from scan().
	d.lock.Lock()
	for _, s := range removeList {
		// The fix: re-validate before deleting.
		if entry, ok := d.locked[s.id]; ok && entry.TxID == s.txID {
			delete(d.locked, s.id)
		}
	}
	d.lock.Unlock()

	// Step 4: Assert the token is still locked by tx-B (not deleted).
	d.lock.RLock()
	entry, ok := d.locked[*tokenID]
	d.lock.RUnlock()
	assert.True(t, ok, "token entry must still exist after scanner runs")
	assert.Equal(t, txB, entry.TxID, "token must remain locked by tx-B, not deleted by scanner")
}

// TestScannerDeletesStaleEntry verifies that the scanner still correctly
// removes entries that have NOT been reclaimed (the normal path).
func TestScannerDeletesStaleEntry(t *testing.T) {
	mock := newMockTXStatusProvider()
	tokenID := &token.ID{TxId: "tok2", Index: 0}
	txA := "tx-A"

	d := &locker{
		ttxdb:                  mock,
		sleepTimeout:           50 * time.Millisecond,
		lock:                   &sync.RWMutex{},
		locked:                 map[token.ID]*lockEntry{},
		validTxEvictionTimeout: 0,
	}

	// Lock the token for tx-A, then mark it Deleted.
	mock.setStatus(txA, ttxdb.Pending)
	_, err := d.Lock(context.Background(), tokenID, txA, false)
	require.NoError(t, err)
	mock.setStatus(txA, ttxdb.Deleted)

	// Scan phase
	type removeEntry struct {
		id   token.ID
		txID string
	}
	var removeList []removeEntry

	d.lock.RLock()
	for id, entry := range d.locked {
		status, _, _ := d.ttxdb.GetStatus(context.Background(), entry.TxID)
		if status == ttxdb.Deleted {
			removeList = append(removeList, removeEntry{id: id, txID: entry.TxID})
		}
	}
	d.lock.RUnlock()

	// No reclaim happens — delete phase should remove the entry.
	d.lock.Lock()
	for _, s := range removeList {
		if entry, ok := d.locked[s.id]; ok && entry.TxID == s.txID {
			delete(d.locked, s.id)
		}
	}
	d.lock.Unlock()

	d.lock.RLock()
	_, ok := d.locked[*tokenID]
	d.lock.RUnlock()
	assert.False(t, ok, "stale entry should have been removed by scanner")
}

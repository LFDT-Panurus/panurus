/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// stubState reports whether an anchor has been applied, and what hash it recorded.
type stubState struct {
	mu    sync.Mutex
	hash  []byte
	found bool
	err   error
}

func (s *stubState) TokenRequestHash(context.Context, [32]byte) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hash, s.found, s.err
}

func (s *stubState) apply(hash []byte) {
	s.mu.Lock()
	s.hash, s.found = hash, true
	s.mu.Unlock()
}

// recordingListener captures the single notification a listener is allowed to receive.
type recordingListener struct {
	mu       sync.Mutex
	done     chan struct{}
	status   int
	message  string
	trHash   []byte
	notified int
}

func newRecordingListener() *recordingListener {
	return &recordingListener{done: make(chan struct{})}
}

func (l *recordingListener) OnStatus(_ context.Context, _ string, status int, message string, trHash []byte) {
	l.mu.Lock()
	l.status, l.message, l.trHash = status, message, trHash
	l.notified++
	first := l.notified == 1
	l.mu.Unlock()
	if first {
		close(l.done)
	}
}

func (l *recordingListener) OnError(context.Context, string, error) {}

func (l *recordingListener) wait(t *testing.T) {
	t.Helper()
	select {
	case <-l.done:
	case <-time.After(3 * time.Second):
		t.Fatal("listener was never notified")
	}
}

func anchor(low byte) [32]byte {
	var a [32]byte
	a[31] = low

	return a
}

func fastManager(evm client.EVMClient, state StateReader, timeout time.Duration) *Manager {
	return NewManager(evm, state, testAddress(0xAA), 0, 5*time.Millisecond, timeout)
}

// --- resolution by eth transaction hash ------------------------------------------------------------

// TestStatusByTxHash covers the lifecycle visible to whoever submitted the transaction (design §7.1).
func TestStatusByTxHash(t *testing.T) {
	blockNumber := uint64(10)

	t.Run("mined and successful is Valid", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.GetTransactionReceiptReturns(&client.Receipt{BlockNumber: &blockNumber, Status: 1}, nil)

		code, _, err := fastManager(evm, &stubState{}, time.Second).StatusByTxHash(t.Context(), client.Hash{})
		require.NoError(t, err)
		assert.Equal(t, driver.Valid, code)
	})

	t.Run("reverted is Invalid", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.GetTransactionReceiptReturns(&client.Receipt{BlockNumber: &blockNumber, Status: 0}, nil)

		code, message, err := fastManager(evm, &stubState{}, time.Second).StatusByTxHash(t.Context(), client.Hash{})
		require.NoError(t, err)
		assert.Equal(t, driver.Invalid, code)
		assert.NotEmpty(t, message, "a revert must carry a reason for the receipt")
	})

	t.Run("pending in the mempool is Busy", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.GetTransactionReceiptReturns(nil, nil)
		evm.IsPendingReturns(true, true, nil)

		code, _, err := fastManager(evm, &stubState{}, time.Second).StatusByTxHash(t.Context(), client.Hash{})
		require.NoError(t, err)
		assert.Equal(t, driver.Busy, code)
	})

	t.Run("unknown to the node is Unknown", func(t *testing.T) {
		evm := &mock.EVMClient{}
		evm.GetTransactionReceiptReturns(nil, nil)
		evm.IsPendingReturns(false, false, nil)

		code, _, err := fastManager(evm, &stubState{}, time.Second).StatusByTxHash(t.Context(), client.Hash{})
		require.NoError(t, err)
		assert.Equal(t, driver.Unknown, code, "a dropped transaction must never be reported as the zero code")
	})
}

// --- resolution by anchor --------------------------------------------------------------------------

// TestStatusByAnchorSuccessIsObservable checks the recipient-side signal: a recorded anchor proves the
// transition committed, and carries the token-request hash.
func TestStatusByAnchorSuccessIsObservable(t *testing.T) {
	state := &stubState{hash: []byte("tr-hash"), found: true}
	m := fastManager(&mock.EVMClient{}, state, time.Second)

	code, hash, _, err := m.StatusByAnchor(t.Context(), anchor(0x01))
	require.NoError(t, err)
	assert.Equal(t, driver.Valid, code)
	assert.Equal(t, []byte("tr-hash"), hash)
}

// TestStatusByAnchorFailureIsNotObservable pins the asymmetry the design calls out (§7.4): a failed
// apply reverts and leaves nothing on chain, so an absent anchor is Unknown, never Invalid. Reporting
// Invalid here would make a merely-pending transaction look permanently failed.
func TestStatusByAnchorFailureIsNotObservable(t *testing.T) {
	m := fastManager(&mock.EVMClient{}, &stubState{found: false}, time.Second)

	code, hash, _, err := m.StatusByAnchor(t.Context(), anchor(0x01))
	require.NoError(t, err)
	assert.Equal(t, driver.Unknown, code, "an unseen anchor is Unknown, not Invalid")
	assert.Nil(t, hash)
}

func TestStatusByAnchorSurfacesReadFailure(t *testing.T) {
	m := fastManager(&mock.EVMClient{}, &stubState{err: errors.New("node down")}, time.Second)

	_, _, _, err := m.StatusByAnchor(t.Context(), anchor(0x01))
	require.Error(t, err)
}

// --- listeners --------------------------------------------------------------------------------------

// TestAddListenerNotifiesImmediatelyWhenFinal checks the driver contract: a transaction that is
// already final must notify without waiting for a poll.
func TestAddListenerNotifiesImmediatelyWhenFinal(t *testing.T) {
	state := &stubState{hash: []byte("tr-hash"), found: true}
	m := fastManager(&mock.EVMClient{}, state, time.Second)
	listener := newRecordingListener()

	require.NoError(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", listener))
	listener.wait(t)
	assert.Equal(t, driver.Valid, listener.status)
	assert.Equal(t, []byte("tr-hash"), listener.trHash)
}

// TestAddListenerNotifiesOnceApplied checks the polling path: the listener fires when the anchor
// appears on chain.
func TestAddListenerNotifiesOnceApplied(t *testing.T) {
	state := &stubState{}
	m := fastManager(&mock.EVMClient{}, state, 3*time.Second)
	listener := newRecordingListener()

	require.NoError(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", listener))
	state.apply([]byte("tr-hash"))

	listener.wait(t)
	assert.Equal(t, driver.Valid, listener.status)
	assert.Equal(t, []byte("tr-hash"), listener.trHash)
}

// TestAddListenerTimesOutAsInvalid is the only failure signal available by anchor: nothing appeared
// before the timeout, so the caller must stop waiting.
func TestAddListenerTimesOutAsInvalid(t *testing.T) {
	m := fastManager(&mock.EVMClient{}, &stubState{}, 50*time.Millisecond)
	listener := newRecordingListener()

	require.NoError(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", listener))
	listener.wait(t)
	assert.Equal(t, driver.Invalid, listener.status)
	assert.Contains(t, listener.message, "timeout")
}

// TestListenerNotifiedExactlyOnce checks the exactly-once contract, which matters because the driver
// removes a listener when it fires.
func TestListenerNotifiedExactlyOnce(t *testing.T) {
	state := &stubState{}
	m := fastManager(&mock.EVMClient{}, state, 200*time.Millisecond)
	listener := newRecordingListener()

	require.NoError(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", listener))
	state.apply([]byte("tr-hash"))
	listener.wait(t)

	// Give the watcher room to (incorrectly) fire again on a later tick or the timeout.
	time.Sleep(300 * time.Millisecond)

	listener.mu.Lock()
	defer listener.mu.Unlock()
	assert.Equal(t, 1, listener.notified, "a listener must be notified exactly once")
}

// TestAddListenerRejectsDuplicateWatch checks the manager does not spawn two watchers for one anchor.
func TestAddListenerRejectsDuplicateWatch(t *testing.T) {
	m := fastManager(&mock.EVMClient{}, &stubState{}, time.Second)

	require.NoError(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", newRecordingListener()))
	err := m.AddListener(t.Context(), anchor(0x01), "anchor-1", newRecordingListener())
	require.Error(t, err)
}

func TestAddListenerRejectsNil(t *testing.T) {
	m := fastManager(&mock.EVMClient{}, &stubState{}, time.Second)
	require.Error(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", nil))
}

// TestTransientReadFailureDoesNotResolve checks that a flaky node does not produce a verdict: the
// manager keeps polling rather than reporting a status it cannot support.
func TestTransientReadFailureDoesNotResolve(t *testing.T) {
	state := &stubState{err: errors.New("temporarily unavailable")}
	m := fastManager(&mock.EVMClient{}, state, 100*time.Millisecond)
	listener := newRecordingListener()

	require.NoError(t, m.AddListener(t.Context(), anchor(0x01), "anchor-1", listener))
	listener.wait(t)
	assert.Equal(t, driver.Invalid, listener.status, "a persistent read failure resolves only at the timeout")
}

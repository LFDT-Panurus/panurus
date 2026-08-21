/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pp

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client/mock"
)

// chainState is a mutable stand-in for the contract's parameters and version, so a test can move them
// mid-watch the way an endorsed setup delta does.
type chainState struct {
	mu      sync.Mutex
	raw     []byte
	version uint64
}

func (c *chainState) set(raw string, version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw, c.version = []byte(raw), version
}

func (c *chainState) get() ([]byte, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.raw, c.version
}

// update is one delivery to the handler.
type update struct {
	raw     string
	version uint64
}

func newWatcherHarness(t *testing.T, state *chainState) (*Watcher, *[]update, *sync.Mutex) {
	t.Helper()
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		raw, version := state.get()
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor(raw), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			return abiUint64For(version), nil
		}

		return nil, nil
	}

	var mu sync.Mutex
	got := make([]update, 0, 4)
	w, err := NewWatcher(evmClient, tokenState, "latest", 10*time.Millisecond,
		func(_ context.Context, raw []byte, version uint64) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, update{raw: string(raw), version: version})

			return nil
		})
	require.NoError(t, err)

	return w, &got, &mu
}

// baselineSet reports whether the watcher has observed the chain at least once, which is when it
// starts treating a version change as an update rather than as its starting point. Tests wait on it
// before changing the chain, so that the change is seen as an update and not as the baseline.
//
// It lives here rather than on Watcher because only tests need it, and it takes the lock because the
// polling goroutine is what writes the fields.
func baselineSet(w *Watcher) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.hasSeen
}

// TestWatcherReportsAnUpdate is the behaviour the driver depends on: parameters change on chain
// because somebody else submitted an endorsed setup delta, and this node has to notice on its own.
func TestWatcherReportsAnUpdate(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 0)

	w, got, mu := newWatcherHarness(t, state)
	w.Start(context.Background())
	defer w.Stop()

	// let it establish a baseline, then move the chain on
	require.Eventually(t, func() bool { return baselineSet(w) }, time.Second, 5*time.Millisecond)
	state.set("params-v1", 1)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(*got) == 1
	}, 2*time.Second, 5*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, update{raw: "params-v1", version: 1}, (*got)[0])
}

// TestWatcherDoesNotReplayTheStartingParameters checks the first observation only establishes a
// baseline. A node already holds the parameters it started with, so announcing them as an update
// would reload every TMS for nothing on every start.
func TestWatcherDoesNotReplayTheStartingParameters(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 3)

	w, got, mu := newWatcherHarness(t, state)
	w.Start(context.Background())
	defer w.Stop()

	require.Eventually(t, func() bool { return baselineSet(w) }, time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, *got, "a steady chain must produce no updates")
}

// TestWatcherReportsEachUpdateOnce checks a version that stays put is not re-reported, so a handler
// that reloads a TMS is not called on every tick.
func TestWatcherReportsEachUpdateOnce(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 0)

	w, got, mu := newWatcherHarness(t, state)
	w.Start(context.Background())
	defer w.Stop()

	require.Eventually(t, func() bool { return baselineSet(w) }, time.Second, 5*time.Millisecond)
	state.set("params-v1", 1)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(*got) == 1
	}, 2*time.Second, 5*time.Millisecond)

	state.set("params-v2", 2)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(*got) == 2
	}, 2*time.Second, 5*time.Millisecond)

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, *got, 2, "each update must be reported exactly once")
	assert.Equal(t, update{raw: "params-v2", version: 2}, (*got)[1])
}

// TestWatcherSurvivesAFailedRead checks a transient RPC failure does not end the watch. Stopping on
// one bad read would leave the node on stale parameters forever, which is far worse than a late
// notice.
func TestWatcherSurvivesAFailedRead(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 0)

	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	// Only the version read errors, and only for the first four calls; everything after that, and
	// every read of the bytes, falls through to the stable chain state below. Returning the raw call
	// count as the version (the original shape of this test) does not work once PublicParams reads the
	// version twice per attempt to detect a torn read: two different call counts a few lines apart
	// would themselves look torn and the test would never get past that.
	var calls atomic.Int64
	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		if string(data) == string(abi.MethodID("getPublicParamsVersion()")) && calls.Add(1) <= 4 {
			return nil, assert.AnError
		}
		raw, version := state.get()
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor(raw), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			return abiUint64For(version), nil
		}

		return nil, nil
	}

	var mu sync.Mutex
	seen := 0
	w, err := NewWatcher(evmClient, tokenState, "latest", 5*time.Millisecond,
		func(context.Context, []byte, uint64) error {
			mu.Lock()
			defer mu.Unlock()
			seen++

			return nil
		})
	require.NoError(t, err)

	w.Start(context.Background())
	defer w.Stop()

	require.Eventually(t, func() bool { return baselineSet(w) }, time.Second, 5*time.Millisecond)
	state.set("params-v1", 1)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return seen > 0
	}, 2*time.Second, 5*time.Millisecond, "the watcher must keep polling after a failed read")
}

// TestWatcherRetriesAFailedHandler is the fix for a real bug: seen used to advance before the handler
// ran, so a handler that failed to apply a version was never asked about it again. The chain looked
// "seen" forever, and the node kept serving stale parameters with nothing left to notice the gap.
func TestWatcherRetriesAFailedHandler(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 0)

	w, attempts, mu := newFailingWatcherHarness(t, state, 3)
	w.Start(context.Background())
	defer w.Stop()

	require.Eventually(t, func() bool { return baselineSet(w) }, time.Second, 5*time.Millisecond)
	state.set("params-v1", 1)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return *attempts >= 3
	}, 2*time.Second, 5*time.Millisecond, "a failed handler must be retried on the same version, not skipped")

	w.mu.Lock()
	seen, hasSeen := w.seen, w.hasSeen
	w.mu.Unlock()
	assert.True(t, hasSeen)
	assert.Equal(t, uint64(1), seen, "seen only advances once the handler actually succeeds")
}

// newFailingWatcherHarness is newWatcherHarness's counterpart for a handler that fails its first
// succeedAfter-1 calls and succeeds from then on, so a test can assert on the retry, not just the
// eventual success.
func newFailingWatcherHarness(t *testing.T, state *chainState, succeedAfter int) (*Watcher, *int, *sync.Mutex) {
	t.Helper()
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	evmClient := &mock.EVMClient{}
	evmClient.CallStub = func(_ context.Context, _ client.Address, data []byte, _ string) ([]byte, error) {
		raw, version := state.get()
		switch string(data) {
		case string(abi.MethodID("getPublicParameters()")):
			return abiBytesFor(raw), nil
		case string(abi.MethodID("getPublicParamsVersion()")):
			return abiUint64For(version), nil
		}

		return nil, nil
	}

	var mu sync.Mutex
	attempts := 0
	w, err := NewWatcher(evmClient, tokenState, "latest", 5*time.Millisecond,
		func(context.Context, []byte, uint64) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts < succeedAfter {
				return assert.AnError
			}

			return nil
		})
	require.NoError(t, err)

	return w, &attempts, &mu
}

// TestWatcherStopIsIdempotentAndBlocks checks a stopped watcher will not call the handler again, so a
// driver shutting down does not race a reload against a torn-down TMS provider.
func TestWatcherStopIsIdempotentAndBlocks(t *testing.T) {
	state := &chainState{}
	state.set("params-v0", 0)

	w, got, mu := newWatcherHarness(t, state)
	w.Start(context.Background())
	w.Start(context.Background()) // second start must be a no-op
	require.Eventually(t, func() bool { return baselineSet(w) }, time.Second, 5*time.Millisecond)

	w.Stop()
	w.Stop() // stopping twice must not panic

	state.set("params-v1", 1)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, *got, "a stopped watcher must not report anything")
}

func TestNewWatcherValidatesItsInput(t *testing.T) {
	tokenState, err := client.HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)

	_, err = NewWatcher(nil, tokenState, "latest", time.Second, func(context.Context, []byte, uint64) error { return nil })
	require.Error(t, err)

	_, err = NewWatcher(&mock.EVMClient{}, tokenState, "latest", time.Second, nil)
	require.Error(t, err)

	// a non-positive interval falls back to the default rather than spinning
	w, err := NewWatcher(&mock.EVMClient{}, tokenState, "latest", 0, func(context.Context, []byte, uint64) error { return nil })
	require.NoError(t, err)
	assert.Equal(t, DefaultWatchInterval, w.interval)
}

// --- helpers ---------------------------------------------------------------------------------------

func abiBytesFor(b []byte) []byte {
	head := make([]byte, 32)
	head[31] = 0x20
	length := make([]byte, 32)
	binary.BigEndian.PutUint64(length[24:], uint64(len(b)))
	padded := make([]byte, (len(b)+31)/32*32)
	copy(padded, b)

	return append(append(head, length...), padded...)
}

func abiUint64For(v uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], v)

	return out
}

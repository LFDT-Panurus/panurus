/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFinalityDB is a finalityDB backed by a real StatusSupport (listener
// registry and notification), with a stubbable batch status lookup.
type fakeFinalityDB struct {
	*common.StatusSupport

	mu               sync.Mutex
	getStatusesStub  func(txIDs []string) (map[string]TxStatus, error)
	getStatusesCalls [][]string
}

func newFakeFinalityDB(stub func(txIDs []string) (map[string]TxStatus, error)) *fakeFinalityDB {
	return &fakeFinalityDB{StatusSupport: common.NewStatusSupport(), getStatusesStub: stub}
}

func (f *fakeFinalityDB) GetStatus(context.Context, string) (TxStatus, string, error) {
	return ttxdb.Pending, "", nil
}

func (f *fakeFinalityDB) GetStatuses(_ context.Context, txIDs []string) (map[string]TxStatus, error) {
	f.mu.Lock()
	f.getStatusesCalls = append(f.getStatusesCalls, append([]string(nil), txIDs...))
	stub := f.getStatusesStub
	f.mu.Unlock()

	return stub(txIDs)
}

func (f *fakeFinalityDB) NotifyStatus(ctx context.Context, txID string, status TxStatus, message string) {
	f.Notify(common.StatusEvent{Ctx: ctx, TxID: txID, ValidationCode: status, ValidationMessage: message})
}

func (f *fakeFinalityDB) calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([][]string(nil), f.getStatusesCalls...)
}

// confirmAll resolves every requested tx id as Confirmed.
func confirmAll(txIDs []string) (map[string]TxStatus, error) {
	out := make(map[string]TxStatus, len(txIDs))
	for _, id := range txIDs {
		out[id] = ttxdb.Confirmed
	}

	return out, nil
}

// TestStatusPoller_SweepCoalescesWaiters verifies that one sweep resolves all
// waited-on transactions with a single batched query and wakes every waiter.
func TestStatusPoller_SweepCoalescesWaiters(t *testing.T) {
	fdb := newFakeFinalityDB(confirmAll)
	ch1 := make(chan common.StatusEvent, 1)
	ch2 := make(chan common.StatusEvent, 1)
	fdb.AddStatusListener("tx1", ch1)
	fdb.AddStatusListener("tx2", ch2)

	newStatusPoller(fdb).sweep()

	calls := fdb.calls()
	require.Len(t, calls, 1, "one sweep over two waiters must issue a single batched query")
	assert.ElementsMatch(t, []string{"tx1", "tx2"}, calls[0])
	for _, ch := range []chan common.StatusEvent{ch1, ch2} {
		select {
		case event := <-ch:
			assert.Equal(t, ttxdb.Confirmed, event.ValidationCode)
		default:
			t.Fatal("waiter not notified by the sweep")
		}
	}
}

// TestStatusPoller_ChunkFailureDoesNotSkipRemainingChunks verifies that a
// failing chunk does not abort the sweep: the remaining chunks are still
// queried and their waiters notified.
func TestStatusPoller_ChunkFailureDoesNotSkipRemainingChunks(t *testing.T) {
	var stubMu sync.Mutex
	call := 0
	fdb := newFakeFinalityDB(nil)
	fdb.getStatusesStub = func(txIDs []string) (map[string]TxStatus, error) {
		stubMu.Lock()
		call++
		failing := call == 1
		stubMu.Unlock()
		if failing {
			return nil, errors.New("transient db error")
		}

		return confirmAll(txIDs)
	}

	const n = statusPollerChunk + 500
	channels := make([]chan common.StatusEvent, n)
	for i := range channels {
		channels[i] = make(chan common.StatusEvent, 1)
		fdb.AddStatusListener(fmt.Sprintf("tx-%d", i), channels[i])
	}

	newStatusPoller(fdb).sweep()

	calls := fdb.calls()
	require.Len(t, calls, 2, "the chunk after the failing one must still be queried")
	require.Len(t, calls[0], statusPollerChunk)
	require.Len(t, calls[1], n-statusPollerChunk)

	notified := 0
	for _, ch := range channels {
		select {
		case <-ch:
			notified++
		default:
		}
	}
	assert.Equal(t, len(calls[1]), notified, "every waiter of the successful chunk must be notified")
}

// TestStatusPoller_UsesSmallestActiveWaiterInterval verifies that the poller
// interval is not fixed by the first caller: a later waiter with a smaller
// polling interval takes effect immediately.
func TestStatusPoller_UsesSmallestActiveWaiterInterval(t *testing.T) {
	fdb := newFakeFinalityDB(confirmAll)

	// the first waiter registers a very slow interval
	unregisterSlow := registerStatusWaiter(fdb, 10*time.Second)
	defer unregisterSlow()

	// a second waiter with a fast interval joins
	ch := make(chan common.StatusEvent, 1)
	fdb.AddStatusListener("tx1", ch)
	defer fdb.DeleteStatusListener("tx1", ch)
	unregisterFast := registerStatusWaiter(fdb, 20*time.Millisecond)
	defer unregisterFast()

	select {
	case event := <-ch:
		assert.Equal(t, ttxdb.Confirmed, event.ValidationCode)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not woken: the poller is stuck on the first caller's interval")
	}
}

// settleOnSlowInterval drives the poller of fdb into a long timer wait: a
// fast waiter is registered next to the caller's slow one, a sweep is
// observed on ch (the loop is demonstrably cycling), the fast waiter is
// unregistered and the sweeps are seen to quiesce — the loop has adopted the
// slow interval and is parked in its timer select.
func settleOnSlowInterval(t *testing.T, fdb *fakeFinalityDB, ch chan common.StatusEvent) {
	t.Helper()

	unregisterFast := registerStatusWaiter(fdb, 10*time.Millisecond)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("poller never swept at the fast interval")
	}
	unregisterFast()

	// sweeps must quiesce: at the fast cadence the call count would keep
	// growing, so a stable count proves the larger interval took over
	require.Eventually(t, func() bool {
		before := len(fdb.calls())
		time.Sleep(100 * time.Millisecond)

		return len(fdb.calls()) == before
	}, 2*time.Second, time.Millisecond, "poller must adopt the larger interval once the fastest waiter leaves")
}

// TestStatusPoller_AdoptsLargerIntervalWhenSmallestLeaves covers the
// re-scheduling branch on unregistration: when the fastest waiter leaves
// while the loop is parked on its timer, the wake-up must abort that timer —
// not a single sweep may fire once only the hour-long waiter remains.
func TestStatusPoller_AdoptsLargerIntervalWhenSmallestLeaves(t *testing.T) {
	fdb := newFakeFinalityDB(confirmAll)

	ch := make(chan common.StatusEvent, 1)
	fdb.AddStatusListener("tx1", ch)
	defer fdb.DeleteStatusListener("tx1", ch)

	unregisterSlow := registerStatusWaiter(fdb, time.Hour)
	defer unregisterSlow()
	unregisterFast := registerStatusWaiter(fdb, 200*time.Millisecond)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("poller never swept at the fast interval")
	}
	// give the loop time to re-arm and park in the select on the next fast
	// timer, so the unregistration below hits a sleeping loop
	time.Sleep(50 * time.Millisecond)

	before := len(fdb.calls())
	unregisterFast()

	time.Sleep(400 * time.Millisecond)
	assert.Len(t, fdb.calls(), before,
		"pending fast timer fired after the fast waiter left: the poller kept the old cadence instead of adopting the larger interval")
}

// TestStatusPoller_StopsWhenIdleAndRestarts verifies that the poller
// goroutine exits promptly once the last waiter unregisters even while it is
// parked in a long timer wait — the exit is driven by the unregister wake-up,
// not by the timer expiring — and starts again when a new waiter registers.
func TestStatusPoller_StopsWhenIdleAndRestarts(t *testing.T) {
	fdb := newFakeFinalityDB(confirmAll)

	ch := make(chan common.StatusEvent, 1)
	fdb.AddStatusListener("tx1", ch)
	defer fdb.DeleteStatusListener("tx1", ch)

	unregisterSlow := registerStatusWaiter(fdb, time.Hour)
	settleOnSlowInterval(t, fdb, ch)

	// the loop is waiting on the hour-long timer; unregistering the last
	// waiter must stop it promptly
	unregisterSlow()

	v, ok := statusPollers.Load(fdb)
	require.True(t, ok)
	p := v.(*statusPoller)
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()

		return !p.running
	}, 2*time.Second, 10*time.Millisecond, "poller must stop when the last waiter unregisters, without waiting out its timer")

	// drain a possibly buffered event from the settle phase, then verify the
	// poller restarts for a new waiter
	select {
	case <-ch:
	default:
	}
	unregister := registerStatusWaiter(fdb, 10*time.Millisecond)
	defer unregister()

	select {
	case event := <-ch:
		assert.Equal(t, ttxdb.Confirmed, event.ValidationCode)
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not restart after going idle")
	}
}

// TestStatusPoller_SustainedRegistrationsDoNotStarveSweep verifies that a
// steady stream of registrations at the same interval does not keep resetting
// the sweep timer: an already-waiting listener must still be notified.
func TestStatusPoller_SustainedRegistrationsDoNotStarveSweep(t *testing.T) {
	fdb := newFakeFinalityDB(confirmAll)

	ch := make(chan common.StatusEvent, 1)
	fdb.AddStatusListener("tx1", ch)
	defer fdb.DeleteStatusListener("tx1", ch)
	unregister := registerStatusWaiter(fdb, 50*time.Millisecond)
	defer unregister()

	// churn: new same-interval waiters keep arriving faster than the sweep
	// interval, like sustained transaction load
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				registerStatusWaiter(fdb, 50*time.Millisecond)()
			}
		}
	}()
	defer func() { close(stop); <-done }()

	select {
	case event := <-ch:
		assert.Equal(t, ttxdb.Confirmed, event.ValidationCode)
	case <-time.After(2 * time.Second):
		t.Fatal("sweep starved: continuous registrations kept resetting the poller timer")
	}
}

// TestStatusPoller_UnregisterIsIdempotent verifies that calling the same
// unregister function twice does not steal the registration of another
// waiter sharing the interval.
func TestStatusPoller_UnregisterIsIdempotent(t *testing.T) {
	fdb := newFakeFinalityDB(confirmAll)

	unregisterA := registerStatusWaiter(fdb, 10*time.Millisecond)

	ch := make(chan common.StatusEvent, 1)
	fdb.AddStatusListener("tx1", ch)
	defer fdb.DeleteStatusListener("tx1", ch)
	unregisterB := registerStatusWaiter(fdb, 10*time.Millisecond)
	defer unregisterB()

	unregisterA()
	unregisterA() // must be a no-op, not decrement B's registration

	select {
	case event := <-ch:
		assert.Equal(t, ttxdb.Confirmed, event.ValidationCode)
	case <-time.After(2 * time.Second):
		t.Fatal("waiter B lost its registration to a double unregister of waiter A")
	}
}

// TestStatusPoller_IsolatedPerDatabase verifies that each database gets its
// own poller: sweeping one database never queries another.
func TestStatusPoller_IsolatedPerDatabase(t *testing.T) {
	fdb1 := newFakeFinalityDB(confirmAll)
	fdb2 := newFakeFinalityDB(confirmAll)

	ch := make(chan common.StatusEvent, 1)
	fdb1.AddStatusListener("tx1", ch)
	defer fdb1.DeleteStatusListener("tx1", ch)
	unregister := registerStatusWaiter(fdb1, 10*time.Millisecond)
	defer unregister()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter on fdb1 not notified")
	}
	assert.Empty(t, fdb2.calls(), "the poller of one database must not query another")
}

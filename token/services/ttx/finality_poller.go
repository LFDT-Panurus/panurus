/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
)

const (
	// statusPollerChunk bounds the number of tx ids per batch status query.
	statusPollerChunk = 1000
	// statusPollerSweepTimeout bounds a single sweep so a stalled database
	// cannot wedge the poller loop.
	statusPollerSweepTimeout = 30 * time.Second
)

// statusPollers holds one statusPoller per finality database instance.
// Finality databases are per-TMS process-level singletons, so entries are
// few and kept for the process lifetime; a poller's goroutine only runs
// while waiters are registered.
var statusPollers sync.Map // finalityDB -> *statusPoller

// statusPoller is the shared fallback poller behind dbFinality: instead of
// every waiter polling GetStatus on its own timer, a single goroutine per
// database batch-fetches the statuses of all waited-on transactions and
// re-publishes terminal ones through the database's listener notification,
// waking the registered waiters. It sweeps at the smallest polling interval
// among the active waiters and stops when none remain.
type statusPoller struct {
	db finalityDB

	mu        sync.Mutex
	intervals map[time.Duration]int // active waiters per requested interval
	running   bool
	wake      chan struct{} // tells the loop to re-evaluate its interval
}

func newStatusPoller(fdb finalityDB) *statusPoller {
	return &statusPoller{db: fdb, intervals: map[time.Duration]int{}, wake: make(chan struct{}, 1)}
}

// registerStatusWaiter registers a waiter polling at the given interval with
// the shared poller of the given database and returns an idempotent
// unregister function.
func registerStatusWaiter(fdb finalityDB, interval time.Duration) func() {
	v, ok := statusPollers.Load(fdb)
	if !ok {
		v, _ = statusPollers.LoadOrStore(fdb, newStatusPoller(fdb))
	}
	p := v.(*statusPoller)
	p.add(interval)

	var once sync.Once

	return func() { once.Do(func() { p.remove(interval) }) }
}

func (p *statusPoller) add(interval time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldMin, had := p.minIntervalLocked()
	p.intervals[interval]++
	if !p.running {
		p.running = true
		go p.run()

		return
	}
	// wake the sleeping loop only when the sweep interval must shrink; waking
	// on every registration would keep resetting the timer and starve sweeps
	// under sustained load
	if !had || interval < oldMin {
		p.signalWake()
	}
}

func (p *statusPoller) remove(interval time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldMin, _ := p.minIntervalLocked()
	if p.intervals[interval]--; p.intervals[interval] <= 0 {
		delete(p.intervals, interval)
	}
	// wake the sleeping loop when the last waiter left (prompt shutdown) or
	// the smallest interval grew (adopt the new one instead of the old timer)
	if newMin, has := p.minIntervalLocked(); !has || newMin > oldMin {
		p.signalWake()
	}
}

// minIntervalLocked returns the smallest interval among the active waiters,
// or false when none is registered. The caller must hold p.mu.
func (p *statusPoller) minIntervalLocked() (time.Duration, bool) {
	var interval time.Duration
	for i := range p.intervals {
		if interval == 0 || i < interval {
			interval = i
		}
	}

	return interval, interval != 0
}

func (p *statusPoller) signalWake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// nextInterval returns the current sweep interval; when no waiter remains it
// marks the poller as stopped and returns false, and the loop must exit.
func (p *statusPoller) nextInterval() (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	interval, ok := p.minIntervalLocked()
	if !ok {
		p.running = false
	}

	return interval, ok
}

func (p *statusPoller) run() {
	for {
		interval, ok := p.nextInterval()
		if !ok {
			return
		}

		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			p.sweep()
		case <-p.wake:
			timer.Stop()
		}
	}
}

// sweep batch-fetches the statuses of every transaction someone is waiting on
// and notifies the terminal ones (Confirmed/Deleted), mirroring what the
// per-waiter poll used to detect. A failing chunk is skipped — the next chunk
// may still succeed and the next sweep retries — unless the sweep context is
// done. Fallback events carry no status message; the live push path does.
func (p *statusPoller) sweep() {
	txIDs := p.db.ListenerTxIDs()
	if len(txIDs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusPollerSweepTimeout)
	defer cancel()

	for chunk := range slices.Chunk(txIDs, statusPollerChunk) {
		statuses, err := p.db.GetStatuses(ctx, chunk)
		if err != nil {
			logger.Warnf("status poller: batch status query failed (%d tx ids): %v", len(chunk), err)
			if ctx.Err() != nil {
				return
			}

			continue
		}
		for txID, status := range statuses {
			if status != ttxdb.Confirmed && status != ttxdb.Deleted {
				continue
			}
			p.db.NotifyStatus(ctx, txID, status, "")
		}
	}
}

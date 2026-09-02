/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// boundedLocker wraps any Locker and enforces a per-transaction lock-count
// ceiling. When the limit is reached, Lock returns an error wrapping
// token.SelectorRateLimited so the selector aborts fast.
type boundedLocker struct {
	Locker
	maxLocksPerTx int
	counts        sync.Map // transaction.ID -> *txLockCounter
}

// txLockCounter tracks how many locks a transaction currently holds, together
// with when it last took one. The timestamp lets stale entries be evicted by
// age instead of wiping every counter, which would hand an in-flight
// transaction a fresh budget.
type txLockCounter struct {
	count atomic.Int64
	// lastLock is the UnixNano of the most recent reservation.
	lastLock atomic.Int64
}

func (c *txLockCounter) touch(now time.Time) { c.lastLock.Store(now.UnixNano()) }

func (c *txLockCounter) staleAt(cutoff time.Time) bool {
	return c.lastLock.Load() < cutoff.UnixNano()
}

// NewBoundedLocker returns a Locker that rejects Lock calls for a given
// consumerTxID once it already holds maxLocksPerTx locks.
// When maxLocksPerTx <= 0 the inner locker is returned unchanged (no wrapping).
func NewBoundedLocker(inner Locker, maxLocksPerTx int) Locker {
	if maxLocksPerTx <= 0 {
		return inner
	}

	return &boundedLocker{Locker: inner, maxLocksPerTx: maxLocksPerTx}
}

func (b *boundedLocker) Lock(ctx context.Context, tokenID *token2.ID, consumerTxID transaction.ID, walletID string) error {
	c := b.counter(consumerTxID)
	limit := int64(b.maxLocksPerTx)
	// Atomically reserve a slot before touching the store. If the CAS fails
	// another goroutine incremented between our Load and Add; retry until the
	// slot is ours or we are over the limit.
	for {
		current := c.count.Load()
		if current >= limit {
			return errors.Wrapf(token.SelectorRateLimited,
				"lock limit exceeded: transaction %s already holds %d locks (max: %d)",
				consumerTxID, current, b.maxLocksPerTx)
		}
		if c.count.CompareAndSwap(current, current+1) {
			break
		}
	}
	c.touch(time.Now())
	if err := b.Locker.Lock(ctx, tokenID, consumerTxID, walletID); err != nil {
		// Roll back the reservation on failure.
		c.count.Add(-1)

		return err
	}

	return nil
}

func (b *boundedLocker) UnlockByTxID(ctx context.Context, consumerTxID transaction.ID) error {
	b.counts.Delete(consumerTxID)

	return b.Locker.UnlockByTxID(ctx, consumerTxID)
}

// ForgetTx drops the lock counter for consumerTxID without touching the store.
// Manager calls it when a selector is closed: after a successful selection the
// locks must survive (the transaction still needs them) but the counter is dead
// weight, and nothing else would ever remove it on a replica that does not win
// cleanup leadership.
func (b *boundedLocker) ForgetTx(consumerTxID transaction.ID) {
	b.counts.Delete(consumerTxID)
}

// EvictStaleTxState drops counters whose last reservation is older than
// olderThan, i.e. transactions whose locks the store itself considers expired.
// It deliberately does not clear every counter: an in-flight transaction that
// legitimately holds N locks would then be handed a fresh budget and could
// acquire maxLocksPerTx more, making the ceiling "per transaction per cleanup
// tick" rather than per transaction.
func (b *boundedLocker) EvictStaleTxState(olderThan time.Duration) {
	cutoff := time.Now().Add(-olderThan)
	b.counts.Range(func(k, v any) bool {
		c, ok := v.(*txLockCounter)
		if !ok {
			b.counts.Delete(k)

			return true
		}
		// Re-check staleness immediately before deleting, and delete only if
		// the entry is still the one inspected, so a reservation taken
		// concurrently is not silently discarded.
		if c.staleAt(cutoff) {
			b.counts.CompareAndDelete(k, v)
		}

		return true
	})
}

func (b *boundedLocker) Cleanup(ctx context.Context, leaseExpiry time.Duration) error {
	// Counter eviction is deliberately not done here: Cleanup only runs on the
	// replica that wins cleanup leadership, while the counters are local to
	// every replica. Manager drives EvictStaleTxState on each tick instead.
	return b.Locker.Cleanup(ctx, leaseExpiry)
}

func (b *boundedLocker) counter(txID transaction.ID) *txLockCounter {
	v, _ := b.counts.LoadOrStore(txID, new(txLockCounter))

	return v.(*txLockCounter) //nolint:forcetypeassert
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
)

// localLockEntry records which consumer transaction this process believes
// owns a token, and when that belief was recorded.
type localLockEntry struct {
	consumerTxID transaction.ID
	lockedAt     time.Time
}

// localLockCache is a process-local, best-effort cache of which tokens this
// replica already knows are locked (by itself or by another transaction in
// the same process), so that repeated candidates within the process's
// lifetime don't need a DB round trip to find out. It is never the source
// of truth: any entry older than leaseExpiry is treated as stale, since the
// DB (via lease expiry or another replica's unlock) may have moved on
// without this process observing it.
//
// Backed by sync.Map: reads of already-populated keys are lock-free, and
// there is no single global mutex to become a bottleneck under contention.
type localLockCache struct {
	entries sync.Map // token2.ID -> localLockEntry
}

func newLocalLockCache() *localLockCache {
	return &localLockCache{}
}

// NewLocalLockCache creates a new, empty process-local lock cache. Exposed
// for callers (e.g. benchmarks) that need to construct one outside a
// Manager.
func NewLocalLockCache() *localLockCache {
	return newLocalLockCache()
}

// lockedBy returns the consumer transaction this process believes owns
// tokenID, and whether that belief is still fresh (i.e. within leaseExpiry).
// A stale or absent entry returns ("", false), meaning the caller must ask
// the DB.
func (c *localLockCache) lockedBy(tokenID token2.ID, leaseExpiry time.Duration) (transaction.ID, bool) {
	v, ok := c.entries.Load(tokenID)
	if !ok {
		return "", false
	}
	entry := v.(localLockEntry)
	if leaseExpiry > 0 && time.Since(entry.lockedAt) >= leaseExpiry {
		return "", false
	}

	return entry.consumerTxID, true
}

// record notes that consumerTxID holds (or just failed to acquire) tokenID
// as of now.
func (c *localLockCache) record(tokenID token2.ID, consumerTxID transaction.ID) {
	c.entries.Store(tokenID, localLockEntry{consumerTxID: consumerTxID, lockedAt: time.Now()})
}

// releaseOwned removes every entry owned by consumerTxID, e.g. after that
// transaction unlocks all of its tokens.
func (c *localLockCache) releaseOwned(consumerTxID transaction.ID) {
	c.entries.Range(func(key, value any) bool {
		if value.(localLockEntry).consumerTxID == consumerTxID {
			c.entries.Delete(key)
		}

		return true
	})
}

// sweepExpired removes every entry older than leaseExpiry, bounding the
// cache's memory to in-flight locks. It complements (does not replace) the
// staleness check in lockedBy, which protects correctness even between
// sweeps.
func (c *localLockCache) sweepExpired(leaseExpiry time.Duration) {
	if leaseExpiry <= 0 {
		return
	}
	now := time.Now()
	c.entries.Range(func(key, value any) bool {
		if now.Sub(value.(localLockEntry).lockedAt) >= leaseExpiry {
			c.entries.Delete(key)
		}

		return true
	})
}

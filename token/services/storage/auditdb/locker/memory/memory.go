/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package memory

import (
	"context"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb/locker/dedup"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"golang.org/x/sync/semaphore"
)

// Locker is the default in-memory Locker. It uses weighted semaphores
// (weight 1) so that AcquireLocks respects context cancellation and deadlines.
// Suitable for single-replica deployments.
type Locker struct {
	locks sync.Map
}

// New returns an empty in-memory Locker ready for use.
func New() *Locker {
	return &Locker{}
}

// AcquireLocks blocks until it holds the lock for every enrollment ID in eIDs,
// then records them under anchor for later release.
//
// Implementation: the enrollment IDs are deduplicated and sorted (see
// dedup.AndSort) so all callers acquire shared locks in the same order and
// cannot deadlock. For each ID it lazily creates a weight-1 semaphore in the
// locks map and acquires it; using semaphore.Acquire (rather than a plain
// Mutex) means a blocked acquisition still honours ctx cancellation/deadline.
// If any acquisition fails, the locks taken so far in this call are released
// and the error is returned, so the call is all-or-nothing. On success the
// sorted ID list is stored under anchor so ReleaseLocks can find it.
func (m *Locker) AcquireLocks(ctx context.Context, anchor string, eIDs ...string) error {
	deduped := dedup.AndSort(eIDs)

	acquired := make([]string, 0, len(deduped))
	for _, id := range deduped {
		sem, _ := m.locks.LoadOrStore(id, semaphore.NewWeighted(1))
		if err := sem.(*semaphore.Weighted).Acquire(ctx, 1); err != nil {
			for _, aid := range acquired {
				if s, ok := m.locks.Load(aid); ok {
					s.(*semaphore.Weighted).Release(1)
				}
			}

			return errors.Wrapf(err, "failed to acquire lock for enrollment ID [%s]", id)
		}
		acquired = append(acquired, id)
	}

	m.locks.Store(anchor, deduped)

	return nil
}

// ReleaseLocks releases every enrollment-ID lock previously acquired under
// anchor. It looks up (and deletes) the sorted ID list stored by AcquireLocks
// and releases each semaphore. It is a no-op if the anchor is unknown (e.g.
// already released), so it is safe to call more than once.
func (m *Locker) ReleaseLocks(_ context.Context, anchor string) {
	dedupBoxed, ok := m.locks.LoadAndDelete(anchor)
	if !ok {
		return
	}
	deduped := dedupBoxed.([]string)
	for _, id := range deduped {
		lock, ok := m.locks.Load(id)
		if !ok {
			continue
		}
		lock.(*semaphore.Weighted).Release(1)
	}
}

// AssertLocksHeld always succeeds for the in-memory locker: locks live in this
// process's memory and cannot be lost or stolen by another replica, so there is
// nothing to re-verify. It exists to satisfy the Locker interface, whose
// distributed implementations use it to detect a lost lease.
func (m *Locker) AssertLocksHeld(_ context.Context, _ string) error {
	return nil
}

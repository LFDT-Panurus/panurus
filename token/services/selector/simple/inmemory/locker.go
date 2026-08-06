/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.uber.org/zap/zapcore"
)

var (
	logger             = logging.MustGetLogger()
	AlreadyLockedError = errors.New("already locked")
	// errShardPruned signals that the shard a lock attempt was working on has
	// been pruned from the registry in the meantime, so the attempt must be
	// retried on a fresh shard. It never escapes Lock.
	errShardPruned = errors.New("shard pruned")
)

const (
	// stopTimeout is the maximum time to wait for the scan goroutine to stop during shutdown.
	// This prevents indefinite blocking if the goroutine fails to exit cleanly.
	stopTimeout = 10 * time.Second
)

var ErrTimeout = errors.New("timeout occurred")

type TXStatusProvider interface {
	GetStatus(ctx context.Context, txID string) (ttxdb.TxStatus, string, error)
}

type lockEntry struct {
	TxID       string
	Identity   string
	Created    time.Time
	LastAccess time.Time
}

func (l lockEntry) String() string {
	return fmt.Sprintf("[[%s][%s] since [%s], last access [%s]]", l.TxID, l.Identity, l.Created, l.LastAccess)
}

// shard holds the lock state for a single owner. Each owner gets its own
// shard so that operations on different owners never block each other.
type shard struct {
	mu     sync.RWMutex
	locked map[token2.ID]*lockEntry
	// pruned reports whether this shard has been removed from the registry.
	// A caller that obtained the shard before it was pruned must not write to
	// it: the entry would be invisible to every other operation. Guarded by mu.
	pruned bool
}

func newShard() *shard {
	return &shard{locked: map[token2.ID]*lockEntry{}}
}

type locker struct {
	ttxdb                  TXStatusProvider
	shardsMu               sync.RWMutex
	shards                 map[string]*shard
	sleepTimeout           time.Duration
	validTxEvictionTimeout time.Duration
	cancel                 context.CancelFunc
	scanDone               chan struct{}
	stopOnce               sync.Once
}

func NewLocker(ttxdb TXStatusProvider, timeout time.Duration, validTxEvictionTimeout time.Duration) simple.Locker {
	ctx, cancel := context.WithCancel(context.Background())

	r := &locker{
		ttxdb:                  ttxdb,
		shards:                 map[string]*shard{},
		sleepTimeout:           timeout,
		validTxEvictionTimeout: validTxEvictionTimeout,
		cancel:                 cancel,
		scanDone:               make(chan struct{}),
	}
	r.start(ctx)

	return r
}

// getOrCreateShard returns the shard for owner, creating it if necessary.
func (d *locker) getOrCreateShard(owner string) *shard {
	d.shardsMu.RLock()
	s, ok := d.shards[owner]
	d.shardsMu.RUnlock()
	if ok {
		return s
	}

	d.shardsMu.Lock()
	defer d.shardsMu.Unlock()
	// Re-check after acquiring the write lock.
	if s, ok = d.shards[owner]; ok {
		return s
	}
	s = newShard()
	d.shards[owner] = s

	return s
}

// Stop cancels the scan goroutine and waits for it to exit.
func (d *locker) Stop() error {
	var err error
	d.stopOnce.Do(func() {
		d.cancel()
		select {
		case <-d.scanDone:
			logger.Debugf("scan goroutine stopped successfully")
		case <-time.After(stopTimeout):
			err = ErrTimeout
			logger.Warnf("scan goroutine did not stop within timeout")
		}
	})

	return err
}

// Lock locks the token id for txID on behalf of owner. owner is the wallet the
// tokens are selected for; each owner has an independent shard so that locking
// for one owner never blocks another.
func (d *locker) Lock(ctx context.Context, owner string, id *token2.ID, txID string, reclaim bool) (string, error) {
	for {
		// The shard may be pruned between getOrCreateShard and the moment we
		// get its write lock; in that case retry on a freshly registered one.
		// This terminates because a shard is only ever pruned while empty.
		holder, err := d.lockInShard(ctx, d.getOrCreateShard(owner), owner, id, txID, reclaim)
		if errors.Is(err, errShardPruned) {
			logger.DebugfContext(ctx, "shard of owner [%s] pruned while locking [%s], retry", owner, id)

			continue
		}

		return holder, err
	}
}

// lockInShard performs the actual locking inside s, the shard of owner. It
// returns errShardPruned if s left the registry before the entry could be
// written, meaning the caller must retry with the current shard of owner.
func (d *locker) lockInShard(ctx context.Context, s *shard, owner string, id *token2.ID, txID string, reclaim bool) (string, error) {
	k := *id

	// check quickly if the token is locked; report the holding transaction, as
	// the caller relies on it to know who to wait for.
	s.mu.RLock()
	if e, ok := s.locked[k]; ok && !reclaim {
		holder := e.TxID
		s.mu.RUnlock()

		return holder, AlreadyLockedError
	}
	s.mu.RUnlock()

	// When reclaiming, resolve the current holder's status before taking the write
	// lock. GetStatus goes to the transaction store, and holding the shard lock
	// across it would make every Lock, UnlockIDs, IsLocked and the collector's own
	// delete phase for this owner wait on that query. The collector already splits
	// its cycle this way for exactly that reason; doing the lookup here under the
	// lock inverted it, so a slow or stuck status provider stalled the whole shard.
	//
	// The holder observed here is re-validated under the lock below, since it may
	// change while the lock is not held.
	var (
		observedTxID   string
		observedStatus int
		statusResolved bool
	)
	if reclaim {
		s.mu.RLock()
		e, held := s.locked[k]
		if held {
			observedTxID = e.TxID
		}
		s.mu.RUnlock()

		if held {
			status, _, err := d.ttxdb.GetStatus(ctx, observedTxID)
			if err != nil {
				logger.DebugfContext(ctx, "failed getting status of [%s] while reclaiming [%s]: [%s]", observedTxID, id, err)
			} else {
				observedStatus, statusResolved = status, true
			}
		}
	}

	// it is either not locked or we are reclaiming
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pruned {
		return "", errShardPruned
	}
	e, ok := s.locked[k]
	if ok {
		e.LastAccess = time.Now()

		if reclaim {
			// Second chance. Only act on the status resolved above if the entry is
			// still the one it was resolved for: a concurrent Lock(reclaim=true) may
			// have handed the token to another transaction in the meantime, and
			// reclaiming on a stale status would take a token away from its new
			// holder. When it no longer matches, report the token as locked and let
			// the caller retry.
			logger.DebugfContext(ctx, "[%s] already locked by [%s], try to reclaim...", id, e)
			status := observedStatus
			reclaimed := statusResolved && e.TxID == observedTxID && status == ttxdb.Deleted
			if reclaimed {
				delete(s.locked, k)
			}
			if !reclaimed {
				logger.DebugfContext(ctx, "[%s] already locked by [%s], reclaim failed, tx status [%s]", id, e, ttxdb.TxStatusMessage[status])
				if logger.IsEnabledFor(zapcore.DebugLevel) {
					return e.TxID, errors.Errorf("already locked by [%s]", e)
				}

				return e.TxID, AlreadyLockedError
			}
			logger.DebugfContext(ctx, "[%s] already locked by [%s], reclaimed successful, tx status [%s]", id, e, ttxdb.TxStatusMessage[status])
		} else {
			logger.DebugfContext(ctx, "[%s] already locked by [%s], no reclaim", id, e)
			if logger.IsEnabledFor(zapcore.DebugLevel) {
				return e.TxID, errors.Errorf("already locked by [%s]", e)
			}

			return e.TxID, AlreadyLockedError
		}
	}

	logger.DebugfContext(ctx, "locking [%s] for [%s] by owner [%s]", id, txID, owner)
	now := time.Now()
	s.locked[k] = &lockEntry{TxID: txID, Identity: owner, Created: now, LastAccess: now}

	return "", nil
}

// UnlockIDs unlocks the passed IDs for the given owner. It returns the list of
// tokens that were not locked in the first place among those passed.
func (d *locker) UnlockIDs(ctx context.Context, owner string, ids ...*token2.ID) []*token2.ID {
	s := d.getOrCreateShard(owner)
	s.mu.Lock()
	defer s.mu.Unlock()

	logger.DebugfContext(ctx, "unlocking tokens [%v]", ids)
	var notFound []*token2.ID
	for _, id := range ids {
		k := *id
		entry, ok := s.locked[k]
		if !ok {
			notFound = append(notFound, &k)
			logger.Warnf("unlocking [%s] hold by no one, skipping", id)

			continue
		}
		logger.DebugfContext(ctx, "unlocking [%s] hold by [%s]", id, entry)
		delete(s.locked, k)
	}

	d.pruneEmptyShard(owner, s)

	return notFound
}

// pruneEmptyShard removes the shard for owner from the registry if it is
// empty, and marks it as pruned so that a Lock still holding a reference to it
// retries on a fresh shard instead of writing an unreachable entry.
//
// The caller must hold s.mu (write lock) so the emptiness check is race-free
// with concurrent locks on the same shard. This is the only place where
// shardsMu is taken while a shard lock is held: shard first, registry second
// is the lock order of this type, and no other path may invert it.
func (d *locker) pruneEmptyShard(owner string, s *shard) {
	if len(s.locked) > 0 {
		return
	}
	d.shardsMu.Lock()
	// Only drop the entry if it still points at this very shard: a newer shard
	// may have been registered for owner while this one sat empty.
	if current, ok := d.shards[owner]; ok && current == s {
		delete(d.shards, owner)
		s.pruned = true
	}
	d.shardsMu.Unlock()
}

// UnlockByTxID unlocks all tokens locked by the given transaction across all owners.
func (d *locker) UnlockByTxID(ctx context.Context, txID string) {
	d.shardsMu.RLock()
	shardsCopy := make(map[string]*shard, len(d.shards))
	maps.Copy(shardsCopy, d.shards)
	d.shardsMu.RUnlock()

	logger.DebugfContext(ctx, "unlocking tokens hold by [%s]", txID)
	for owner, s := range shardsCopy {
		s.mu.Lock()
		for id, entry := range s.locked {
			if entry.TxID == txID {
				logger.DebugfContext(ctx, "unlocking [%s] hold by [%s]", id, entry)
				delete(s.locked, id)
			}
		}
		d.pruneEmptyShard(owner, s)
		s.mu.Unlock()
	}
}

// IsLocked reports whether id is locked by any owner.
func (d *locker) IsLocked(id *token2.ID) bool {
	d.shardsMu.RLock()
	shardsCopy := make([]*shard, 0, len(d.shards))
	for _, s := range d.shards {
		shardsCopy = append(shardsCopy, s)
	}
	d.shardsMu.RUnlock()

	for _, s := range shardsCopy {
		s.mu.RLock()
		_, ok := s.locked[*id]
		s.mu.RUnlock()
		if ok {
			return true
		}
	}

	return false
}

func (d *locker) start(ctx context.Context) {
	go d.scan(ctx)
}

// lockedCount returns the total number of locked tokens across all owners.
// It snapshots the shards and releases shardsMu before taking any shard lock:
// holding shardsMu here would invert the shard-then-registry lock order of
// pruneEmptyShard and deadlock against it.
func (d *locker) lockedCount() int {
	d.shardsMu.RLock()
	shardsCopy := make([]*shard, 0, len(d.shards))
	for _, s := range d.shards {
		shardsCopy = append(shardsCopy, s)
	}
	d.shardsMu.RUnlock()

	total := 0
	for _, s := range shardsCopy {
		s.mu.RLock()
		total += len(s.locked)
		s.mu.RUnlock()
	}

	return total
}

func (d *locker) scan(ctx context.Context) {
	defer close(d.scanDone)
	for {
		// Check for shutdown before starting a new scan cycle.
		select {
		case <-ctx.Done():
			logger.Debugf("token collector: stopping")

			return
		default:
		}
		logger.DebugfContext(ctx, "token collector: scan locked tokens")

		// Snapshot the current shards so we don't hold shardsMu during the
		// (potentially slow) status lookups.
		d.shardsMu.RLock()
		shardsCopy := make(map[string]*shard, len(d.shards))
		maps.Copy(shardsCopy, d.shards)
		d.shardsMu.RUnlock()

		// Snapshot of an entry as observed during the inspection phase. The
		// txID and last access time are kept so the delete phase can
		// re-validate the entry (prevents a TOCTOU race with Lock/reclaim).
		type observedEntry struct {
			id         token2.ID
			txID       string
			lastAccess time.Time
		}

		for owner, s := range shardsCopy {
			// Copy the entries and release the shard lock before looking their
			// status up: the lookups may be slow, and no Lock/UnlockIDs of this
			// owner must ever wait behind the collector on the status provider.
			s.mu.RLock()
			observed := make([]observedEntry, 0, len(s.locked))
			for id, entry := range s.locked {
				observed = append(observed, observedEntry{id: id, txID: entry.TxID, lastAccess: entry.LastAccess})
			}
			s.mu.RUnlock()

			removeList := make([]observedEntry, 0, len(observed))
			for _, entry := range observed {
				status, _, err := d.ttxdb.GetStatus(ctx, entry.txID)
				if err != nil {
					logger.Warnf("failed getting status for token [%s] locked by [%s], remove", entry.id, entry.txID)
					removeList = append(removeList, entry)

					continue
				}
				switch status {
				case ttxdb.Confirmed:
					// remove only if elapsed enough time from last access, to avoid concurrency issue
					if time.Since(entry.lastAccess) > d.validTxEvictionTimeout {
						removeList = append(removeList, entry)
						logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], time elapsed, remove", entry.id, entry.txID, ttxdb.TxStatusMessage[status])
					}
				case ttxdb.Deleted:
					removeList = append(removeList, entry)
					logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], remove", entry.id, entry.txID, ttxdb.TxStatusMessage[status])
				default:
					logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], skip", entry.id, entry.txID, ttxdb.TxStatusMessage[status])
				}
			}

			s.mu.Lock()
			logger.DebugfContext(ctx, "token collector: freeing [%d] items from shard [%s]", len(removeList), owner)
			for _, entry := range removeList {
				// Re-validate: only delete if the entry is still the one that
				// was inspected. While the shard was unlocked, a
				// Lock(reclaim=true) may have re-locked the token for another
				// transaction, or a plain Lock may have refreshed its last
				// access time; either way the entry must be kept.
				if e, ok := s.locked[entry.id]; ok && e.TxID == entry.txID && e.LastAccess.Equal(entry.lastAccess) {
					delete(s.locked, entry.id)
				}
			}
			d.pruneEmptyShard(owner, s)
			s.mu.Unlock()
		}

		for {
			logger.DebugfContext(ctx, "token collector: sleep for some time...")
			select {
			case <-time.After(d.sleepTimeout):
			case <-ctx.Done():
				logger.Debugf("token collector: stopping during sleep")

				return
			}
			if l := d.lockedCount(); l > 0 {
				// time to do some token collection
				logger.DebugfContext(ctx, "token collector: time to do some token collection, [%d] locked", l)

				break
			}
		}
	}
}

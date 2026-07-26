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
	Created    time.Time
	LastAccess time.Time
}

func (l lockEntry) String() string {
	return fmt.Sprintf("[[%s] since [%s], last access [%s]]", l.TxID, l.Created, l.LastAccess)
}

// shard holds the lock state for the tokens of a single owner. A token has
// exactly one owner, so operations on different owners can never contend
// for the same token and each shard can be guarded independently.
type shard struct {
	mu     sync.Mutex
	locked map[token2.ID]*lockEntry
	// dead is set (under mu) when the scanner prunes this empty shard from
	// the registry; a caller that raced the pruning and still holds a
	// reference must retry the registry lookup instead of using it.
	dead bool
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
		sleepTimeout:           timeout,
		shards:                 map[string]*shard{},
		validTxEvictionTimeout: validTxEvictionTimeout,
		cancel:                 cancel,
		scanDone:               make(chan struct{}),
	}
	r.start(ctx)

	return r
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

// shard returns the shard for the given owner, creating it on first use.
// The empty owner is a valid key: callers without owner context share one
// default shard, which degrades to the pre-sharding single-map behavior.
func (d *locker) shard(owner string) *shard {
	d.shardsMu.RLock()
	s, ok := d.shards[owner]
	d.shardsMu.RUnlock()
	if ok {
		return s
	}

	d.shardsMu.Lock()
	defer d.shardsMu.Unlock()
	if s, ok := d.shards[owner]; ok {
		return s
	}
	s = &shard{locked: map[token2.ID]*lockEntry{}}
	d.shards[owner] = s

	return s
}

// lockShard returns the owner's shard with its mutex held. If the shard was
// pruned between the registry lookup and acquiring its mutex, the lookup is
// retried, so callers never operate on a shard that left the registry.
func (d *locker) lockShard(owner string) *shard {
	for {
		s := d.shard(owner)
		s.mu.Lock()
		if !s.dead {
			return s
		}
		s.mu.Unlock()
	}
}

// allShards returns a snapshot of the current shards keyed by owner.
func (d *locker) allShards() map[string]*shard {
	d.shardsMu.RLock()
	defer d.shardsMu.RUnlock()
	shards := make(map[string]*shard, len(d.shards))
	maps.Copy(shards, d.shards)

	return shards
}

func (d *locker) Lock(ctx context.Context, owner string, id *token2.ID, txID string, reclaim bool) (string, error) {
	k := *id
	s := d.lockShard(owner)
	defer s.mu.Unlock()
	e, ok := s.locked[k]
	if ok {
		e.LastAccess = time.Now()

		if reclaim {
			// Second chance
			logger.DebugfContext(ctx, "[%s] already locked by [%s], try to reclaim...", id, e)
			reclaimed, status := d.reclaim(ctx, s, id, e.TxID)
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
	logger.DebugfContext(ctx, "locking [%s] for [%s]", id, txID)
	now := time.Now()
	s.locked[k] = &lockEntry{TxID: txID, Created: now, LastAccess: now}

	return "", nil
}

// UnlockIDs unlocks the passed IDS of the given owner. It returns the list of tokens that were
// not locked in the first place among those passed.
func (d *locker) UnlockIDs(ctx context.Context, owner string, ids ...*token2.ID) []*token2.ID {
	s := d.lockShard(owner)
	defer s.mu.Unlock()

	logger.DebugfContext(ctx, "unlocking tokens [%v]", ids)
	var notFound []*token2.ID
	for _, id := range ids {
		k := *id
		entry, ok := s.locked[k]
		if !ok {
			notFound = append(notFound, &k)
			logger.Warnf("unlocking [%s] hold by no one, skipping [%s]", id, entry)

			continue
		}
		logger.DebugfContext(ctx, "unlocking [%s] hold by [%s]", id, entry)
		delete(s.locked, k)
	}

	return notFound
}

// UnlockByTxID unlocks all tokens locked by the given transaction. The owner
// is unknown at this point, so every shard is visited, each locked briefly.
func (d *locker) UnlockByTxID(ctx context.Context, txID string) {
	logger.DebugfContext(ctx, "unlocking tokens hold by [%s]", txID)
	for _, s := range d.allShards() {
		s.mu.Lock()
		for id, entry := range s.locked {
			if entry.TxID == txID {
				logger.DebugfContext(ctx, "unlocking [%s] hold by [%s]", id, entry)
				delete(s.locked, id)
			}
		}
		s.mu.Unlock()
	}
}

func (d *locker) IsLocked(id *token2.ID) bool {
	k := *id
	for _, s := range d.allShards() {
		s.mu.Lock()
		_, ok := s.locked[k]
		s.mu.Unlock()
		if ok {
			return true
		}
	}

	return false
}

// reclaim must be called while holding the shard's mutex.
func (d *locker) reclaim(ctx context.Context, s *shard, id *token2.ID, txID string) (bool, int) {
	status, _, err := d.ttxdb.GetStatus(ctx, txID)
	if err != nil {
		return false, status
	}
	switch status {
	case ttxdb.Deleted:
		delete(s.locked, *id)

		return true, status
	default:
		return false, status
	}
}

func (d *locker) start(ctx context.Context) {
	go d.scan(ctx)
}

// lockedCount returns the total number of locked tokens across all shards.
func (d *locker) lockedCount() int {
	total := 0
	for _, s := range d.allShards() {
		s.mu.Lock()
		total += len(s.locked)
		s.mu.Unlock()
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
		d.scanShards(ctx)

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

// scanShards runs one collection cycle over every shard. For each shard it
// snapshots the entries under the shard lock, resolves their transaction
// statuses without holding any lock, and then deletes stale entries under
// the shard lock again — re-validating that each entry still belongs to the
// transaction observed during the snapshot, since a concurrent
// Lock(reclaim=true) may have re-locked the token for a new transaction in
// the meantime (TOCTOU).
func (d *locker) scanShards(ctx context.Context) {
	type scannedEntry struct {
		id         token2.ID
		txID       string
		lastAccess time.Time
	}
	type removeEntry struct {
		id        token2.ID
		txID      string
		confirmed bool
	}

	for owner, s := range d.allShards() {
		// Phase 1: snapshot the shard's entries.
		s.mu.Lock()
		entries := make([]scannedEntry, 0, len(s.locked))
		for id, entry := range s.locked {
			entries = append(entries, scannedEntry{id: id, txID: entry.TxID, lastAccess: entry.LastAccess})
		}
		s.mu.Unlock()

		// Phase 2: resolve statuses without holding the shard lock, so the
		// owner's Lock/Unlock operations are not blocked behind status lookups.
		var removeList []removeEntry
		for _, entry := range entries {
			status, _, err := d.ttxdb.GetStatus(ctx, entry.txID)
			if err != nil {
				logger.Warnf("failed getting status for token [%s] locked by [%s], remove", entry.id, entry.txID)
				removeList = append(removeList, removeEntry{id: entry.id, txID: entry.txID})

				continue
			}
			switch status {
			case ttxdb.Confirmed:
				// remove only if elapsed enough time from last access, to avoid concurrency issue
				if time.Since(entry.lastAccess) > d.validTxEvictionTimeout {
					removeList = append(removeList, removeEntry{id: entry.id, txID: entry.txID, confirmed: true})
					logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], time elapsed, remove", entry.id, entry.txID, ttxdb.TxStatusMessage[status])
				}
			case ttxdb.Deleted:
				removeList = append(removeList, removeEntry{id: entry.id, txID: entry.txID})
				logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], remove", entry.id, entry.txID, ttxdb.TxStatusMessage[status])
			default:
				logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], skip", entry.id, entry.txID, ttxdb.TxStatusMessage[status])
			}
		}

		// Phase 3: delete, re-validating each entry.
		s.mu.Lock()
		logger.DebugfContext(ctx, "token collector: freeing [%d] items", len(removeList))
		for _, r := range removeList {
			entry, ok := s.locked[r.id]
			if !ok || entry.TxID != r.txID {
				continue
			}
			if r.confirmed && time.Since(entry.LastAccess) <= d.validTxEvictionTimeout {
				// the entry was accessed again after the snapshot; keep it
				continue
			}
			delete(s.locked, r.id)
		}
		s.mu.Unlock()

		d.pruneIfEmpty(owner, s)
	}
}

// pruneIfEmpty removes the owner's shard from the registry when it holds no
// locks. Both the registry lock and the shard lock are held while re-checking
// emptiness and marking the shard dead, so a concurrent Lock either finds the
// shard still registered or observes dead and retries the registry lookup —
// a live lock can never end up in a pruned shard.
func (d *locker) pruneIfEmpty(owner string, s *shard) {
	s.mu.Lock()
	empty := len(s.locked) == 0
	s.mu.Unlock()
	if !empty {
		// common case: skip the exclusive registry lock entirely
		return
	}

	d.shardsMu.Lock()
	defer d.shardsMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.locked) == 0 && d.shards[owner] == s {
		s.dead = true
		delete(d.shards, owner)
	}
}

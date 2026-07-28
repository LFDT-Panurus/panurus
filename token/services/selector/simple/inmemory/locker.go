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
	s := d.getOrCreateShard(owner)
	k := *id

	// check quickly if the token is locked
	s.mu.RLock()
	if _, ok := s.locked[k]; ok && !reclaim {
		s.mu.RUnlock()

		return "", AlreadyLockedError
	}
	s.mu.RUnlock()

	// it is either not locked or we are reclaiming
	s.mu.Lock()
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
// empty. The caller must hold s.mu (write lock) so the emptiness check is
// race-free with concurrent locks on the same shard.
func (d *locker) pruneEmptyShard(owner string, s *shard) {
	if len(s.locked) > 0 {
		return
	}
	d.shardsMu.Lock()
	// Re-check under both locks: a concurrent Lock may have inserted an
	// entry between our len==0 check and acquiring shardsMu.
	if len(s.locked) == 0 {
		delete(d.shards, owner)
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

// reclaim checks the tx status for id inside shard s and deletes the entry
// if the holding transaction is finalized (Deleted). The caller must hold
// s.mu (write lock).
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

func (d *locker) lockedCount() int {
	d.shardsMu.RLock()
	defer d.shardsMu.RUnlock()

	total := 0
	for _, s := range d.shards {
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

		// Track both token ID and the txID that was observed during the scan,
		// so we can re-validate before deleting (prevents TOCTOU race with Lock/reclaim).
		type removeEntry struct {
			id   token2.ID
			txID string
		}

		for owner, s := range shardsCopy {
			var removeList []removeEntry

			s.mu.RLock()
			for id, entry := range s.locked {
				status, _, err := d.ttxdb.GetStatus(ctx, entry.TxID)
				if err != nil {
					logger.Warnf("failed getting status for token [%s] locked by [%s], remove", id, entry)
					removeList = append(removeList, removeEntry{id: id, txID: entry.TxID})

					continue
				}
				switch status {
				case ttxdb.Confirmed:
					// remove only if elapsed enough time from last access, to avoid concurrency issue
					if time.Since(entry.LastAccess) > d.validTxEvictionTimeout {
						removeList = append(removeList, removeEntry{id: id, txID: entry.TxID})
						logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], time elapsed, remove", id, entry, ttxdb.TxStatusMessage[status])
					}
				case ttxdb.Deleted:
					removeList = append(removeList, removeEntry{id: id, txID: entry.TxID})
					logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], remove", id, entry, ttxdb.TxStatusMessage[status])
				default:
					logger.DebugfContext(ctx, "token [%s] locked by [%s] in status [%s], skip", id, entry, ttxdb.TxStatusMessage[status])
				}
			}
			s.mu.RUnlock()

			s.mu.Lock()
			logger.DebugfContext(ctx, "token collector: freeing [%d] items from shard [%s]", len(removeList), owner)
			for _, entry := range removeList {
				// Re-validate: only delete if the entry still belongs to the same
				// transaction that was inspected during the RLock scan phase.
				// Between RUnlock and Lock, a Lock(reclaim=true) call may have
				// reclaimed this token and re-locked it for a new transaction.
				if e, ok := s.locked[entry.id]; ok && e.TxID == entry.txID {
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

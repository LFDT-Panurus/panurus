/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"fmt"
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

type locker struct {
	ttxdb                  TXStatusProvider
	lock                   *sync.RWMutex
	locked                 map[token2.ID]*lockEntry
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
		lock:                   &sync.RWMutex{},
		locked:                 map[token2.ID]*lockEntry{},
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

func (d *locker) Lock(ctx context.Context, id *token2.ID, txID string, reclaim bool) (string, error) {
	k := *id

	// check quickly if the token is locked
	d.lock.RLock()
	if _, ok := d.locked[k]; ok && !reclaim {
		// return immediately
		d.lock.RUnlock()

		return "", AlreadyLockedError
	}
	d.lock.RUnlock()

	// it is either not locked or we are reclaiming
	d.lock.Lock()
	defer d.lock.Unlock()
	e, ok := d.locked[k]
	if ok {
		e.LastAccess = time.Now()

		if reclaim {
			// Second chance
			logger.DebugfContext(ctx, "[%s] already locked by [%s], try to reclaim...", id, e)
			reclaimed, status := d.reclaim(ctx, id, e.TxID)
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
	d.locked[k] = &lockEntry{TxID: txID, Created: now, LastAccess: now}

	return "", nil
}

// UnlockIDs unlocks the passed IDS. It returns the list of tokens that were not locked in the first place among
// those passed.
func (d *locker) UnlockIDs(ctx context.Context, ids ...*token2.ID) []*token2.ID {
	d.lock.Lock()
	defer d.lock.Unlock()

	logger.DebugfContext(ctx, "unlocking tokens [%v]", ids)
	var notFound []*token2.ID
	for _, id := range ids {
		k := *id
		entry, ok := d.locked[k]
		if !ok {
			notFound = append(notFound, &k)
			logger.Warnf("unlocking [%s] hold by no one, skipping [%s]", id, entry)

			continue
		}
		logger.DebugfContext(ctx, "unlocking [%s] hold by [%s]", id, entry)
		delete(d.locked, k)
	}

	return notFound
}

func (d *locker) UnlockByTxID(ctx context.Context, txID string) {
	d.lock.Lock()
	defer d.lock.Unlock()

	logger.DebugfContext(ctx, "unlocking tokens hold by [%s]", txID)
	for id, entry := range d.locked {
		if entry.TxID == txID {
			logger.DebugfContext(ctx, "unlocking [%s] hold by [%s]", id, entry)
			delete(d.locked, id)
		}
	}
}

func (d *locker) IsLocked(id *token2.ID) bool {
	d.lock.Lock()
	defer d.lock.Unlock()

	_, ok := d.locked[*id]

	return ok
}

func (d *locker) reclaim(ctx context.Context, id *token2.ID, txID string) (bool, int) {
	status, _, err := d.ttxdb.GetStatus(ctx, txID)
	if err != nil {
		return false, status
	}
	switch status {
	case ttxdb.Deleted:
		delete(d.locked, *id)

		return true, status
	default:
		return false, status
	}
}

func (d *locker) start(ctx context.Context) {
	go d.scan(ctx)
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
		// Track both token ID and the txID that was observed during the scan,
		// so we can re-validate before deleting (prevents TOCTOU race with Lock/reclaim).
		type removeEntry struct {
			id   token2.ID
			txID string
		}
		var removeList []removeEntry
		d.lock.RLock()
		for id, entry := range d.locked {
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
		d.lock.RUnlock()

		d.lock.Lock()
		logger.DebugfContext(ctx, "token collector: freeing [%d] items", len(removeList))
		for _, s := range removeList {
			// Re-validate: only delete if the entry still belongs to the same
			// transaction that was inspected during the RLock scan phase.
			// Between RUnlock and Lock, a Lock(reclaim=true) call may have
			// reclaimed this token and re-locked it for a new transaction.
			if entry, ok := d.locked[s.id]; ok && entry.TxID == s.txID {
				delete(d.locked, s.id)
			}
		}
		d.lock.Unlock()

		for {
			logger.DebugfContext(ctx, "token collector: sleep for some time...")
			select {
			case <-time.After(d.sleepTimeout):
			case <-ctx.Done():
				logger.Debugf("token collector: stopping during sleep")

				return
			}
			d.lock.RLock()
			l := len(d.locked)
			d.lock.RUnlock()
			if l > 0 {
				// time to do some token collection
				logger.DebugfContext(ctx, "token collector: time to do some token collection, [%d] locked", l)

				break
			}
		}
	}
}

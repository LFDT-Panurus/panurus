/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	lazy2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

const (
	// stopTimeout is the maximum time to wait for the cleaner goroutine to stop during shutdown.
	// This prevents indefinite blocking if the goroutine fails to exit cleanly.
	stopTimeout = 10 * time.Second
)

var ErrTimeout = errors.New("timeout occurred")

type Manager struct {
	selectorCache          lazy2.Provider[transaction.ID, TokenSelectorUnlocker]
	locker                 Locker
	leaseExpiry            time.Duration
	leaseCleanupTickPeriod time.Duration
	metrics                *Metrics
	cancel                 context.CancelFunc
	cleanerDone            chan struct{}
	stopOnce               sync.Once
}

func NewManager(
	fetcher TokenFetcher,
	locker Locker,
	precision uint64,
	backoff time.Duration,
	maxRetriesAfterBackOff int,
	leaseExpiry time.Duration,
	leaseCleanupTickPeriod time.Duration,
	m *Metrics,
) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &Manager{
		locker:                 locker,
		leaseExpiry:            leaseExpiry,
		leaseCleanupTickPeriod: leaseCleanupTickPeriod,
		metrics:                m,
		cancel:                 cancel,
		cleanerDone:            make(chan struct{}),
		selectorCache: lazy2.NewProvider(func(txID transaction.ID) (TokenSelectorUnlocker, error) {
			return NewSherdSelector(txID, fetcher, locker, precision, backoff, maxRetriesAfterBackOff, m), nil
		}),
	}
	if leaseCleanupTickPeriod > 0 && leaseExpiry > 0 {
		go mgr.cleaner(ctx)
	} else {
		close(mgr.cleanerDone)
	}

	return mgr
}

func (m *Manager) NewSelector(id transaction.ID) (token.Selector, error) {
	return m.selectorCache.Get(id)
}

func (m *Manager) Unlock(ctx context.Context, id transaction.ID) error {
	return m.locker.UnlockByTxID(ctx, id)
}

func (m *Manager) Close(id transaction.ID) error {
	if c, ok := m.selectorCache.Delete(id); ok {
		return c.Close()
	}

	return errors.New("selector for " + id + " not found")
}

func (m *Manager) cleaner(ctx context.Context) {
	defer close(m.cleanerDone)
	ticker := time.NewTicker(m.leaseCleanupTickPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			logger.DebugfContext(ctx, "release token locks older than [%s]", m.leaseExpiry)
			if err := m.locker.Cleanup(ctx, m.leaseExpiry); err != nil {
				logger.Errorf("failed to release token locks: [%s]", err)
			}
		case <-ctx.Done():
			logger.Debugf("cleaner stopping")

			return
		}
	}
}

// Stop cancels the cleaner goroutine and waits for it to exit.
func (m *Manager) Stop() error {
	var err error
	m.stopOnce.Do(func() {
		m.cancel()
		select {
		case <-m.cleanerDone:
			logger.Debugf("cleaner goroutine stopped successfully")
		case <-time.After(stopTimeout):
			err = ErrTimeout
			logger.Warnf("cleaner goroutine did not stop within timeout")
		}
	})

	return err
}

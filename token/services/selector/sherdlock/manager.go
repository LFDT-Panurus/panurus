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

// txScopedState is implemented by Lockers that keep replica-local
// per-transaction bookkeeping alongside the shared lock store — currently
// boundedLocker's per-transaction lock counters. Because the state is local, it
// cannot be reclaimed by the shared Cleanup path (which only one replica runs);
// Manager drives its lifecycle explicitly instead.
type txScopedState interface {
	// ForgetTx drops the state for a transaction that is done with selection.
	ForgetTx(txID transaction.ID)
	// EvictStaleTxState drops state whose transaction has been idle longer
	// than olderThan, as a backstop for selectors that are never closed.
	EvictStaleTxState(olderThan time.Duration)
}

// Config holds all configuration parameters for the Manager
type Config struct {
	Fetcher                TokenFetcher
	Locker                 Locker
	Precision              uint64
	Backoff                time.Duration
	MaxRetriesAfterBackOff int
	LeaseExpiry            time.Duration
	LeaseCleanupTickPeriod time.Duration
	MaxTokensPerSelection  int
	MaxLockAttempts        int
	SelectionTimeout       time.Duration
	Metrics                *Metrics
}

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

func NewManager(cfg *Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &Manager{
		locker:                 cfg.Locker,
		leaseExpiry:            cfg.LeaseExpiry,
		leaseCleanupTickPeriod: cfg.LeaseCleanupTickPeriod,
		metrics:                cfg.Metrics,
		cancel:                 cancel,
		cleanerDone:            make(chan struct{}),
		selectorCache: lazy2.NewProvider(func(txID transaction.ID) (TokenSelectorUnlocker, error) {
			return NewSherdSelector(txID, cfg.Fetcher, cfg.Locker, cfg.Precision, cfg.Backoff, cfg.MaxRetriesAfterBackOff, cfg.MaxTokensPerSelection, cfg.MaxLockAttempts, cfg.SelectionTimeout, cfg.Metrics), nil
		}),
	}
	if cfg.LeaseCleanupTickPeriod > 0 && cfg.LeaseExpiry > 0 {
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
	// Release replica-local per-transaction bookkeeping. The locks themselves
	// must stay: after a successful selection the transaction still needs them,
	// and they are released by Unlock/UnlockByTxID or by lease expiry.
	if s, ok := m.locker.(txScopedState); ok {
		s.ForgetTx(id)
	}

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
			m.runCleanupTick(ctx)
		case <-ctx.Done():
			logger.Debugf("cleaner stopping")

			return
		}
	}
}

// runCleanupTick acquires cleanup leadership for this tick so only one
// replica runs Cleanup at a time; other replicas skip the tick. See #1798.
func (m *Manager) runCleanupTick(ctx context.Context) {
	// Evict stale replica-local state first, unconditionally: it belongs to
	// this process, so gating it behind cleanup leadership would let it grow
	// without bound on every replica that never wins the lease.
	if s, ok := m.locker.(txScopedState); ok {
		s.EvictStaleTxState(m.leaseExpiry)
	}

	leadership, acquired, err := m.locker.AcquireCleanupLeadership(ctx)
	if err != nil {
		logger.Errorf("failed to acquire cleanup leadership: [%s]", err)

		return
	}
	if !acquired {
		logger.DebugfContext(ctx, "cleanup leadership not acquired, skipping tick")

		return
	}
	defer func() {
		if err := leadership.Close(); err != nil {
			logger.Warnf("failed to release cleanup leadership: [%s]", err)
		}
	}()

	logger.DebugfContext(ctx, "release token locks older than [%s]", m.leaseExpiry)
	if err := m.locker.Cleanup(ctx, m.leaseExpiry); err != nil {
		logger.Errorf("failed to release token locks: [%s]", err)
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

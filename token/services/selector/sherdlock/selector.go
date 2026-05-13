/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

const (
	// This way we avoid deadlocks, e.g. We have 2 tokens of value 10CHF each (20 CHF in total).
	// We also have two processes that both ask for 15CHF. If both of them concurrently lock one token each,
	// they will retry maxRetry times to see if the other process in the meantime unlocked the token.
	// If not, to avoid locking these tokens forever, we roll back and unlock the tokens.
	maxImmediateRetries = 5
	NoBackoff           = -1
)

var logger = logging.MustGetLogger()

func Logger() logging.Logger {
	return logger
}

type Selector struct {
	logger    logging.Logger
	cache     Iterator[*token2.UnspentTokenInWallet]
	fetcher   TokenFetcher
	locker    TokenLocker
	precision uint64
	metrics   *Metrics
	mu        sync.Mutex   // protects cache pointer reads/writes
	selectMu  sync.RWMutex // RLock held during selection; Close takes write lock to drain in-flight selects

	// Resource limits to prevent algorithmic attacks
	maxTokensPerSelection int
	maxLockAttempts       int
	selectionTimeout      time.Duration
}

type StubbornSelector struct {
	*Selector
	// After maxImmediateRetries attempts, the procs will roll back and unlock the tokens.
	// If two procs unlock at the same time, we have a livelock.
	// To avoid it, we back off (wait) for a random interval within some limits and retry
	backoffInterval time.Duration
	// However, it might be that we don't have a livelock, but we are simply out of funds.
	// Instead of polling forever, we can abort after a certain amount of attempts.
	maxRetriesAfterBackoff int
}

func (m *StubbornSelector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	start := time.Now()

	// Create timeout context if configured
	timeoutCtx, cancel := withSelectionTimeout(ctx, m.selectionTimeout)
	defer cancel()

	var tokensIterated, lockAttempts int
	for retriesAfterBackoff := 0; retriesAfterBackoff <= m.maxRetriesAfterBackoff; retriesAfterBackoff++ {
		if tokens, quantity, ti, la, err := m.selectWithoutMetrics(timeoutCtx, ownerFilter, q, tokenType); err == nil || !errors.Is(err, token.SelectorSufficientButLockedFunds) {
			tokensIterated += ti
			lockAttempts += la
			m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())

			// Check if we hit the selector's own timeout (not the caller's context cancellation).
			if errors.Is(err, context.DeadlineExceeded) {
				if unlockErr := m.locker.UnlockAll(ctx); unlockErr != nil {
					m.logger.Errorf("failed to unlock tokens after timeout: %s", unlockErr)
				}
				m.metrics.SelectionOutcome.With(outcomeLabel, "timeout").Add(1)

				return nil, nil, errors.Wrapf(
					token.SelectorTimedOut,
					"token selection aborted: exceeded timeout (%v) after examining %d tokens and %d lock attempts",
					m.selectionTimeout, tokensIterated, lockAttempts,
				)
			}

			// Unlock on any error (except success)
			if err != nil {
				if unlockErr := m.locker.UnlockAll(ctx); unlockErr != nil {
					m.logger.Errorf("failed to unlock tokens after selection error: %s", unlockErr)
				}
			}

			if err == nil {
				m.metrics.SelectionOutcome.With(outcomeLabel, "success").Add(1)
			} else if errors.Is(err, token.SelectorInsufficientFunds) {
				m.metrics.SelectionOutcome.With(outcomeLabel, "insufficient_funds").Add(1)
			} else {
				m.metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)
			}

			return tokens, quantity, err
		} else {
			tokensIterated += ti
			lockAttempts += la

			// Release the tokens we did manage to lock before backing off.
			// The whole point of the backoff is to let a competing selection
			// complete, which it cannot do while we sit on part of the funds:
			// two selections that each hold a subset would both spin until
			// their retry budget is exhausted and both report insufficient
			// funds, with the funds available the entire time (the livelock
			// described in the maxImmediateRetries comment above).
			if unlockErr := m.locker.UnlockAll(ctx); unlockErr != nil {
				m.logger.Errorf("failed to unlock tokens before backoff: %s", unlockErr)
			}
		}
		var backoffDuration time.Duration
		if m.backoffInterval > 0 {
			backoffDuration = time.Duration(rand.Int64N(int64(m.backoffInterval)))
		}
		m.logger.DebugfContext(ctx,
			"Token selection aborted, so that other procs can retry. Release tokens and backoff for %v before retrying to select. "+
				"In the meantime maybe some other process releases token locks or adds tokens.",
			backoffDuration)
		select {
		case <-time.After(backoffDuration):
		case <-timeoutCtx.Done():
			if err := m.locker.UnlockAll(ctx); err != nil {
				m.logger.Errorf("failed to unlock tokens on context cancellation: %s", err)
			}
			m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())

			// If the caller's context is still alive, the timeout is our own selectionTimeout.
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				m.metrics.SelectionOutcome.With(outcomeLabel, "timeout").Add(1)

				return nil, nil, errors.Wrapf(
					token.SelectorTimedOut,
					"token selection aborted: exceeded timeout (%v) after examining %d tokens and %d lock attempts",
					m.selectionTimeout, tokensIterated, lockAttempts,
				)
			}

			m.metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)

			return nil, nil, timeoutCtx.Err()
		}
		m.logger.DebugfContext(ctx, "Now it is our turn to retry...")
	}

	m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
	m.metrics.SelectionOutcome.With(outcomeLabel, "locked_funds").Add(1)

	return nil, nil, errors.Wrapf(token.SelectorInsufficientFunds, "aborted too many times and no other process unlocked or added tokens")
}

func NewStubbornSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, backoff time.Duration,
	retries int, maxTokensPerSelection int, maxLockAttempts int, selectionTimeout time.Duration, m *Metrics) *StubbornSelector {
	return &StubbornSelector{
		Selector:               NewSelector(logger, tokenDB, lockDB, precision, maxTokensPerSelection, maxLockAttempts, selectionTimeout, m),
		backoffInterval:        backoff,
		maxRetriesAfterBackoff: retries,
	}
}

func NewSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, maxTokensPerSelection int, maxLockAttempts int, selectionTimeout time.Duration, m *Metrics) *Selector {
	return &Selector{
		logger:                logger,
		cache:                 collections.NewEmptyIterator[*token2.UnspentTokenInWallet](),
		fetcher:               tokenDB,
		locker:                lockDB,
		precision:             precision,
		maxTokensPerSelection: maxTokensPerSelection,
		maxLockAttempts:       maxLockAttempts,
		selectionTimeout:      selectionTimeout,
		metrics:               m,
	}
}

func (s *Selector) Select(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	start := time.Now()

	// Create timeout context if configured
	timeoutCtx, cancel := withSelectionTimeout(ctx, s.selectionTimeout)
	defer cancel()

	ids, quantity, immediateRetries, tokensIterated, lockAttempts, err := s.selectInternal(timeoutCtx, owner, q, tokenType)

	// Check if we hit the timeout
	if errors.Is(err, context.DeadlineExceeded) {
		// Use original context for cleanup to ensure it completes
		if err2 := s.locker.UnlockAll(ctx); err2 != nil {
			s.logger.Warnf("failed to unlock tokens after timeout: %v", err2)
		}
		s.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
		s.metrics.SelectionOutcome.With(outcomeLabel, "timeout").Add(1)

		return nil, nil, errors.Wrapf(
			token.SelectorTimedOut,
			"token selection aborted: exceeded timeout (%v) after examining %d tokens and %d lock attempts",
			s.selectionTimeout, tokensIterated, lockAttempts,
		)
	}

	if err != nil {
		if err2 := s.locker.UnlockAll(ctx); err2 != nil {
			s.logger.Warnf("failed to unlock tokens after selection error: %v", err2)
		}
	}
	s.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
	s.metrics.ImmediateRetries.Observe(float64(immediateRetries))
	if err == nil {
		s.metrics.SelectionOutcome.With(outcomeLabel, "success").Add(1)
	} else if errors.Is(err, token.SelectorSufficientButLockedFunds) {
		s.metrics.SelectionOutcome.With(outcomeLabel, "locked_funds").Add(1)
	} else if errors.Is(err, token.SelectorInsufficientFunds) {
		s.metrics.SelectionOutcome.With(outcomeLabel, "insufficient_funds").Add(1)
	} else {
		s.metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)
	}

	return ids, quantity, err
}

// selectWithoutMetrics is used by StubbornSelector to avoid double-counting metrics.
// Note: Does not call UnlockAll on error - the caller is responsible for cleanup.
// This avoids attempting to unlock with a cancelled context.
func (s *Selector) selectWithoutMetrics(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, int, int, error) {
	ids, quantity, _, tokensIterated, lockAttempts, err := s.selectInternal(ctx, owner, q, tokenType)

	return ids, quantity, tokensIterated, lockAttempts, err
}

func (s *Selector) selectInternal(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, int, int, int, error) {
	// Hold the read side of selectMu for the duration of the iteration so that
	// Close() (which takes the write side) waits for in-flight iterations
	// instead of calling cache.Close() while cache.Next() is reading from
	// sql.Rows.
	//
	// This has to live here rather than in Selector.Select: StubbornSelector
	// overrides Select and reaches selectInternal via selectWithoutMetrics
	// without ever going through Selector.Select, and NewSherdSelector returns
	// a StubbornSelector for any backoff >= 0 — the default production path.
	// Guarding here also keeps the lock off the backoff sleeps, so Close() is
	// not blocked for the whole retry budget.
	s.selectMu.RLock()
	defer s.selectMu.RUnlock()

	// Take mu to snapshot s.cache into a local variable, then release the lock
	// and work exclusively through the local copy. This eliminates the data race
	// between selectInternal reads of s.cache and a concurrent Close() write.
	// On the cache-reload path we take mu again to publish the new iterator so
	// that a concurrent Close() can still call Close() on it.
	s.mu.Lock()
	cache := s.cache
	s.mu.Unlock()
	if cache == nil {
		return nil, nil, 0, 0, 0, errors.Errorf("selector is already closed")
	}

	quantity, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, 0, 0, 0, errors.Wrapf(err, "failed to create quantity")
	}

	sum, selected, tokensLockedByOthersExist, immediateRetries := token2.NewZeroQuantity(s.precision), collections.NewSet[*token2.ID](), true, 0
	tokensIterated, lockAttempts := 0, 0
	for {
		if t, err := cache.Next(); err != nil {
			return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Wrapf(err, "failed to get tokens for [%s:%s]", owner.ID(), tokenType)
		} else if t == nil {
			if !tokensLockedByOthersExist {
				return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Wrapf(
					token.SelectorInsufficientFunds,
					"insufficient funds, only [%s] tokens of type [%s] are available, but [%s] were requested and no other process has any tokens locked",
					sum.Decimal(),
					tokenType,
					quantity.Decimal(),
				)
			}

			if immediateRetries > maxImmediateRetries {
				s.logger.Warnf("Exceeded max number of immediate retries. Unlock tokens and abort...")

				// When we loop over the tokens, we check whether a token is already locked.
				// Every time our token cache finishes, but we noted that one of the tokens we saw was used by someone,
				// we retry to fetch, in case the other process did not spend and unlocked the token meanwhile.
				// We do not unlock our tokens, yet.
				// After some retries, we unlock the tokens and return a token.SelectorInsufficientFunds error
				return nil, nil, immediateRetries, tokensIterated, lockAttempts, token.SelectorSufficientButLockedFunds
			}

			s.logger.DebugfContext(ctx, "Fetch all non-deleted tokens from the DB and refresh the token cache.")
			newCache, fetchErr := s.fetcher.UnspentTokensIteratorBy(ctx, owner.ID(), tokenType, 0)
			if fetchErr != nil {
				return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Wrapf(fetchErr, "failed to reload tokens for retry %d [%s:%s]", immediateRetries, owner.ID(), tokenType)
			}
			if err := s.swapCache(newCache); err != nil {
				return nil, nil, immediateRetries, tokensIterated, lockAttempts, err
			}
			cache = newCache

			immediateRetries++
			tokensLockedByOthersExist = false
		} else {
			// Check token iteration limit (only count actual tokens, not nil)
			tokensIterated++
			if tokensIterated > s.maxTokensPerSelection {
				return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Errorf(
					"token selection aborted: exceeded max token iteration limit (%d tokens)",
					s.maxTokensPerSelection,
				)
			}

			// Check lock attempt limit
			lockAttempts++
			if lockAttempts > s.maxLockAttempts {
				return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Errorf(
					"token selection aborted: exceeded max lock attempts (%d) after examining %d tokens",
					s.maxLockAttempts, tokensIterated,
				)
			}

			if locked, lockErr := s.locker.TryLock(ctx, &t.Id, owner.ID()); !locked {
				// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
				if errors.Is(lockErr, token.SelectorRateLimited) {
					return nil, nil, immediateRetries, tokensIterated, lockAttempts, lockErr
				}
				s.logger.DebugfContext(ctx, "Tried to lock token [%v], but it was already locked by another process", t)
				tokensLockedByOthersExist = true
			} else {
				s.logger.DebugfContext(ctx, "Got the lock on token [%v]", t)
				q, err := token2.ToQuantity(t.Quantity, s.precision)
				if err != nil {
					return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Wrapf(err, "invalid token [%s] found", t.Id)
				}
				s.logger.DebugfContext(ctx, "Found token [%s] to add: [%s:%s].", t.Id, q.Decimal(), t.Type)
				immediateRetries = 0
				sum, err = sum.Add(q)
				if err != nil {
					return nil, nil, immediateRetries, tokensIterated, lockAttempts, errors.Wrapf(err, "failed to add quantity")
				}
				selected.Add(&t.Id)
				if sum.Cmp(quantity) >= 0 {
					return selected.ToSlice(), sum, immediateRetries, tokensIterated, lockAttempts, nil
				}
			}
		}
	}
}

// withSelectionTimeout bounds ctx by the configured selection timeout.
//
// A non-positive timeout means "no timeout": passing it straight to
// context.WithTimeout yields an already-expired context, so every Select would
// return SelectorTimedOut after examining zero tokens. Manager takes its
// limits from a Config struct, which makes an omitted SelectionTimeout easy to
// miss, so the zero value has to be harmless.
func withSelectionTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, timeout)
}

// swapCache installs it as the new token cache and closes the iterator it
// replaces, so a refresh on retry does not abandon a database cursor and its
// pooled connection. If the selector was closed in the meantime, it closes it
// too and reports an error: no iterator is ever left unclosed.
func (s *Selector) swapCache(it Iterator[*token2.UnspentTokenInWallet]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache == nil {
		it.Close()

		return errors.New("selector is already closed")
	}
	s.cache.Close()
	s.cache = it

	return nil
}

func (s *Selector) Close() error {
	// Acquire the write side of selectMu to drain any in-flight Select()
	// calls before touching the iterator. This prevents cache.Close() from
	// racing with cache.Next() inside selectInternal.
	s.selectMu.Lock()
	defer s.selectMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache == nil {
		return errors.New("selector is already closed")
	}
	s.cache.Close()
	s.cache = nil

	return nil
}

func (s *Selector) UnlockAll(ctx context.Context) error {
	return s.locker.UnlockAll(ctx)
}

func tokenKey(walletID string, typ token2.Type) string {
	return fmt.Sprintf("%s.%s", walletID, typ)
}

type locker struct {
	Locker
	txID transaction.ID
}

func (l *locker) TryLock(ctx context.Context, tokenID *token2.ID, walletID string) (bool, error) {
	err := l.Lock(ctx, tokenID, l.txID, walletID)
	if err != nil {
		logger.DebugfContext(ctx, "failed to lock [%v] for [%s]: [%s]", tokenID, l.txID, err)
	}

	return err == nil, err
}

func (l *locker) UnlockAll(ctx context.Context) error {
	return l.UnlockByTxID(ctx, l.txID)
}

func NewSherdSelector(txID transaction.ID, fetcher TokenFetcher, lockDB Locker, precision uint64, backoff time.Duration,
	maxRetriesAfterBackoff int, maxTokensPerSelection int, maxLockAttempts int, selectionTimeout time.Duration, m *Metrics) TokenSelectorUnlocker {
	logger := logger.Named("selector-" + txID)
	locker := &locker{txID: txID, Locker: lockDB}
	if backoff < 0 {
		return NewSelector(logger, fetcher, locker, precision, maxTokensPerSelection, maxLockAttempts, selectionTimeout, m)
	} else {
		return NewStubbornSelector(logger, fetcher, locker, precision, backoff, maxRetriesAfterBackoff, maxTokensPerSelection, maxLockAttempts, selectionTimeout, m)
	}
}

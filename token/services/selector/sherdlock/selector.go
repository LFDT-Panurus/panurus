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
	mu        sync.Mutex // protects cache field for concurrent Close() calls
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

// recordStubbornSelectionOutcome records the outcome metric for a (final) selection attempt's
// result.
func recordStubbornSelectionOutcome(metrics *Metrics, err error) {
	switch {
	case err == nil:
		metrics.SelectionOutcome.With(outcomeLabel, "success").Add(1)
	case errors.Is(err, token.SelectorInsufficientFunds):
		metrics.SelectionOutcome.With(outcomeLabel, "insufficient_funds").Add(1)
	default:
		metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)
	}
}

func (m *StubbornSelector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	start := time.Now()
	for retriesAfterBackoff := 0; retriesAfterBackoff <= m.maxRetriesAfterBackoff; retriesAfterBackoff++ {
		if tokens, quantity, err := m.selectWithoutMetrics(ctx, ownerFilter, q, tokenType); err == nil || !errors.Is(err, token.SelectorSufficientButLockedFunds) {
			m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
			recordStubbornSelectionOutcome(m.metrics, err)

			return tokens, quantity, err
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
		case <-ctx.Done():
			if err := m.locker.UnlockAll(ctx); err != nil {
				m.logger.Errorf("failed to unlock tokens on context cancellation: %s", err)
			}
			m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
			m.metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)

			return nil, nil, ctx.Err()
		}
		m.logger.DebugfContext(ctx, "Now it is our turn to retry...")
	}

	m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
	m.metrics.SelectionOutcome.With(outcomeLabel, "locked_funds").Add(1)

	return nil, nil, errors.Wrapf(token.SelectorInsufficientFunds, "aborted too many times and no other process unlocked or added tokens")
}

func NewStubbornSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, backoff time.Duration, retries int, m *Metrics) *StubbornSelector {
	return &StubbornSelector{
		Selector:               NewSelector(logger, tokenDB, lockDB, precision, m),
		backoffInterval:        backoff,
		maxRetriesAfterBackoff: retries,
	}
}

func NewSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, m *Metrics) *Selector {
	return &Selector{
		logger:    logger,
		cache:     collections.NewEmptyIterator[*token2.UnspentTokenInWallet](),
		fetcher:   tokenDB,
		locker:    lockDB,
		precision: precision,
		metrics:   m,
	}
}

func (s *Selector) Select(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	start := time.Now()
	ids, quantity, immediateRetries, err := s.selectInternal(ctx, owner, q, tokenType)
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
func (s *Selector) selectWithoutMetrics(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	ids, quantity, _, err := s.selectInternal(ctx, owner, q, tokenType)
	if err != nil {
		if err2 := s.locker.UnlockAll(ctx); err2 != nil {
			s.logger.Warnf("failed to unlock tokens after selection error: %v", err2)
		}
	}

	return ids, quantity, err
}

// handleExhaustedCache handles the "cache has no more tokens" case: it either fails with
// insufficient funds (no tokens are locked elsewhere), aborts with
// token.SelectorSufficientButLockedFunds after too many immediate retries, or reloads the token
// cache and reports the next immediateRetries count for another pass.
func (s *Selector) handleExhaustedCache(
	ctx context.Context,
	owner token.OwnerFilter,
	tokenType token2.Type,
	sum, quantity token2.Quantity,
	tokensLockedByOthersExist bool,
	immediateRetries int,
) (newCache Iterator[*token2.UnspentTokenInWallet], newImmediateRetries int, err error) {
	if !tokensLockedByOthersExist {
		return nil, immediateRetries, errors.Wrapf(
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
		return nil, immediateRetries, token.SelectorSufficientButLockedFunds
	}

	s.logger.DebugfContext(ctx, "Fetch all non-deleted tokens from the DB and refresh the token cache.")
	newCache, err = s.fetcher.UnspentTokensIteratorBy(ctx, owner.ID(), tokenType)
	if err != nil {
		return nil, immediateRetries, errors.Wrapf(err, "failed to reload tokens for retry %d [%s:%s]", immediateRetries, owner.ID(), tokenType)
	}

	return newCache, immediateRetries + 1, nil
}

// tryAddToken attempts to lock and add token t to the selection. It reports whether t was
// locked by another process (in which case it wasn't added), the updated running sum, and
// whether enough has now been selected (done).
func (s *Selector) tryAddToken(
	ctx context.Context,
	owner token.OwnerFilter,
	t *token2.UnspentTokenInWallet,
	sum, quantity token2.Quantity,
	selected collections.Set[*token2.ID],
) (lockedByOther bool, newSum token2.Quantity, done bool, err error) {
	locked, lockErr := s.locker.TryLock(ctx, &t.Id, owner.ID())
	if !locked {
		// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
		if errors.Is(lockErr, token.SelectorRateLimited) {
			return false, sum, false, lockErr
		}
		s.logger.DebugfContext(ctx, "Tried to lock token [%v], but it was already locked by another process", t)

		return true, sum, false, nil
	}

	s.logger.DebugfContext(ctx, "Got the lock on token [%v]", t)
	q, err := token2.ToQuantity(t.Quantity, s.precision)
	if err != nil {
		return false, sum, false, errors.Wrapf(err, "invalid token [%s] found", t.Id)
	}
	s.logger.DebugfContext(ctx, "Found token [%s] to add: [%s:%s].", t.Id, q.Decimal(), t.Type)
	newSum, err = sum.Add(q)
	if err != nil {
		return false, sum, false, errors.Wrapf(err, "failed to add quantity")
	}
	selected.Add(&t.Id)

	return false, newSum, newSum.Cmp(quantity) >= 0, nil
}

func (s *Selector) selectInternal(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, int, error) {
	if s.isClosed() {
		return nil, nil, 0, errors.Errorf("selector is already closed")
	}
	quantity, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed to create quantity")
	}
	sum, selected, tokensLockedByOthersExist, immediateRetries := token2.NewZeroQuantity(s.precision), collections.NewSet[*token2.ID](), true, 0

	return s.runSelectLoop(ctx, owner, quantity, tokenType, sum, selected, tokensLockedByOthersExist, immediateRetries)
}

// selectLoopStep is the outcome of one iteration of runSelectLoop's selection loop.
type selectLoopStep struct {
	sum                       token2.Quantity
	tokensLockedByOthersExist bool
	immediateRetries          int
	done                      bool
	err                       error
}

// stepSelectLoop runs a single iteration of the token-selection loop: it pulls the next token
// from s.cache (refreshing the cache if it's exhausted) and, if found, tries to lock and add it.
func (s *Selector) stepSelectLoop(
	ctx context.Context,
	owner token.OwnerFilter,
	quantity token2.Quantity,
	tokenType token2.Type,
	sum token2.Quantity,
	selected collections.Set[*token2.ID],
	tokensLockedByOthersExist bool,
	immediateRetries int,
) selectLoopStep {
	t, err := s.next()
	if err != nil {
		return selectLoopStep{err: errors.Wrapf(err, "failed to get tokens for [%s:%s]", owner.ID(), tokenType)}
	}

	if t == nil {
		newImmediateRetries, err := s.refreshOnExhaustedCache(ctx, owner, tokenType, sum, quantity, tokensLockedByOthersExist, immediateRetries)
		if err != nil {
			return selectLoopStep{immediateRetries: immediateRetries, err: err}
		}

		return selectLoopStep{sum: sum, tokensLockedByOthersExist: false, immediateRetries: newImmediateRetries}
	}

	lockedByOther, newSum, done, err := s.tryAddToken(ctx, owner, t, sum, quantity, selected)
	if err != nil {
		return selectLoopStep{immediateRetries: immediateRetries, err: err}
	}
	if lockedByOther {
		return selectLoopStep{sum: sum, tokensLockedByOthersExist: true, immediateRetries: immediateRetries}
	}

	return selectLoopStep{sum: newSum, tokensLockedByOthersExist: tokensLockedByOthersExist, immediateRetries: 0, done: done}
}

// runSelectLoop drives stepSelectLoop until enough tokens are selected or an error occurs.
func (s *Selector) runSelectLoop(
	ctx context.Context,
	owner token.OwnerFilter,
	quantity token2.Quantity,
	tokenType token2.Type,
	sum token2.Quantity,
	selected collections.Set[*token2.ID],
	tokensLockedByOthersExist bool,
	immediateRetries int,
) ([]*token2.ID, token2.Quantity, int, error) {
	for {
		step := s.stepSelectLoop(ctx, owner, quantity, tokenType, sum, selected, tokensLockedByOthersExist, immediateRetries)
		if step.err != nil {
			return nil, nil, step.immediateRetries, step.err
		}
		sum = step.sum
		tokensLockedByOthersExist = step.tokensLockedByOthersExist
		immediateRetries = step.immediateRetries
		if step.done {
			return selected.ToSlice(), sum, immediateRetries, nil
		}
	}
}

// refreshOnExhaustedCache calls handleExhaustedCache and, on success, installs the refreshed
// cache on s before returning the new immediateRetries count.
func (s *Selector) refreshOnExhaustedCache(ctx context.Context, owner token.OwnerFilter, tokenType token2.Type, sum, quantity token2.Quantity, tokensLockedByOthersExist bool, immediateRetries int) (int, error) {
	newCache, newImmediateRetries, err := s.handleExhaustedCache(ctx, owner, tokenType, sum, quantity, tokensLockedByOthersExist, immediateRetries)
	if err != nil {
		return immediateRetries, err
	}
	if err := s.swapCache(newCache); err != nil {
		return immediateRetries, err
	}

	return newImmediateRetries, nil
}

// next returns the next token of the current cache. It holds s.mu for the whole
// call so that a concurrent Close cannot swap the iterator out, or nil it, while
// it is being read. It reports an error if the selector has already been closed.
func (s *Selector) next() (*token2.UnspentTokenInWallet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache == nil {
		return nil, errors.New("selector is already closed")
	}

	return s.cache.Next()
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache == nil {
		return errors.New("selector is already closed")
	}
	s.cache.Close()
	s.cache = nil

	return nil
}

func (s *Selector) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cache == nil
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

func NewSherdSelector(txID transaction.ID, fetcher TokenFetcher, lockDB Locker, precision uint64, backoff time.Duration, maxRetriesAfterBackoff int, m *Metrics) TokenSelectorUnlocker {
	logger := logger.Named("selector-" + txID)
	locker := &locker{txID: txID, Locker: lockDB}
	if backoff < 0 {
		return NewSelector(logger, fetcher, locker, precision, m)
	} else {
		return NewStubbornSelector(logger, fetcher, locker, precision, backoff, maxRetriesAfterBackoff, m)
	}
}

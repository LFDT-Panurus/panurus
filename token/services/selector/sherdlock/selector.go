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
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
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
	// exactMatch enables the change-avoidance pre-search (currently k=1): before the
	// greedy walk, prefer a single unlocked candidate whose amount equals the request.
	exactMatch bool
}

// Option customizes a Selector at construction time.
type Option func(*Selector)

// WithExactMatch enables (or disables) the exact-amount change-avoidance pre-search.
// It is off by default, preserving the plain greedy first-fit behaviour.
func WithExactMatch(enabled bool) Option {
	return func(s *Selector) { s.exactMatch = enabled }
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
	// One set for the whole call: each backoff round runs a fresh inner selection, but
	// the histogram reports per-Select() fan-out, so the distinct tokens seen across
	// every round are unioned here and observed exactly once.
	attempted := collections.NewSet[token2.ID]()
	defer observeDistinctTokensAttempted(m.metrics, attempted)
	for retriesAfterBackoff := 0; retriesAfterBackoff <= m.maxRetriesAfterBackoff; retriesAfterBackoff++ {
		if tokens, quantity, err := m.selectWithoutMetrics(ctx, ownerFilter, q, tokenType, attempted); err == nil || !errors.Is(err, token.SelectorSufficientButLockedFunds) {
			m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
			if err == nil {
				m.metrics.SelectionOutcome.With(outcomeLabel, "success").Add(1)
			} else if errors.Is(err, token.SelectorInsufficientFunds) {
				m.metrics.SelectionOutcome.With(outcomeLabel, "insufficient_funds").Add(1)
			} else {
				m.metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)
			}

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

func NewStubbornSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, backoff time.Duration, retries int, m *Metrics, opts ...Option) *StubbornSelector {
	return &StubbornSelector{
		Selector:               NewSelector(logger, tokenDB, lockDB, precision, m, opts...),
		backoffInterval:        backoff,
		maxRetriesAfterBackoff: retries,
	}
}

func NewSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, m *Metrics, opts ...Option) *Selector {
	s := &Selector{
		logger:    logger,
		cache:     collections.NewEmptyIterator[*token2.UnspentTokenInWallet](),
		fetcher:   tokenDB,
		locker:    lockDB,
		precision: precision,
		metrics:   m,
	}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// observeDistinctTokensAttempted records the per-Select() fan-out. It skips the
// observation when no lock was ever attempted: the early bails in selectInternal and the
// plain empty-wallet path reach here with an empty set, and observing 0 would land under
// the histogram's first bucket while still inflating _count - pulling the mean and the
// quantiles toward zero and blurring the very signal this histogram exists to give. No
// attempt is absence of data, not a measurement of zero.
func observeDistinctTokensAttempted(m *Metrics, attempted collections.Set[token2.ID]) {
	if n := attempted.Length(); n > 0 {
		m.DistinctTokensAttempted.Observe(float64(n))
	}
}

func (s *Selector) Select(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	start := time.Now()
	attempted := collections.NewSet[token2.ID]()
	defer observeDistinctTokensAttempted(s.metrics, attempted)
	ids, quantity, immediateRetries, err := s.selectInternal(ctx, owner, q, tokenType, attempted)
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
func (s *Selector) selectWithoutMetrics(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type, attempted collections.Set[token2.ID]) ([]*token2.ID, token2.Quantity, error) {
	ids, quantity, _, err := s.selectInternal(ctx, owner, q, tokenType, attempted)
	if err != nil {
		if err2 := s.locker.UnlockAll(ctx); err2 != nil {
			s.logger.Warnf("failed to unlock tokens after selection error: %v", err2)
		}
	}

	return ids, quantity, err
}

// selectInternal performs one selection attempt. attempted is owned by the caller and
// records every distinct token this attempt tried to lock; a StubbornSelector reuses the
// same set across all of its backoff rounds so that DistinctTokensAttempted is observed
// once per Select() call, counting each distinct token once.
func (s *Selector) selectInternal(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type, attempted collections.Set[token2.ID]) ([]*token2.ID, token2.Quantity, int, error) {
	if s.isClosed() {
		return nil, nil, 0, errors.Errorf("selector is already closed")
	}
	quantity, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed to create quantity")
	}

	// Change-avoidance pre-search (k=1): before the greedy walk commits any tokens,
	// prefer a single unlocked candidate whose amount equals the full request, which
	// completes the selection with zero change. On any miss we fall through to the
	// greedy walk below, unchanged.
	if s.exactMatch {
		s.metrics.ExactMatchAttempts.Add(1)
		if ids, sum, ok := s.trySingleTokenExactMatch(ctx, owner, quantity, tokenType, attempted); ok {
			s.metrics.ExactMatchHits.Add(1)
			s.logger.DebugfContext(ctx, "exact-match pre-search selected a single token of [%s:%s]", quantity.Decimal(), tokenType)

			return ids, sum, 0, nil
		}
		s.metrics.ExactMatchMisses.Add(1)
	}

	sum, selected, tokensLockedByOthersExist, immediateRetries := token2.NewZeroQuantity(s.precision), collections.NewSet[*token2.ID](), true, 0
	for {
		if t, err := s.next(); err != nil {
			return nil, nil, immediateRetries, errors.Wrapf(err, "failed to get tokens for [%s:%s]", owner.ID(), tokenType)
		} else if t == nil {
			if !tokensLockedByOthersExist {
				return nil, nil, immediateRetries, errors.Wrapf(
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
				return nil, nil, immediateRetries, token.SelectorSufficientButLockedFunds
			}

			s.logger.DebugfContext(ctx, "Fetch all non-deleted tokens from the DB and refresh the token cache.")
			it, err := s.fetcher.UnspentTokensIteratorBy(ctx, owner.ID(), tokenType)
			if err != nil {
				return nil, nil, immediateRetries, errors.Wrapf(err, "failed to reload tokens for retry %d [%s:%s]", immediateRetries, owner.ID(), tokenType)
			}
			if err := s.swapCache(it); err != nil {
				return nil, nil, immediateRetries, err
			}

			immediateRetries++
			tokensLockedByOthersExist = false
		} else {
			// Counted once here, before the outcome is known, so a later third
			// outcome branch cannot forget to record the attempt.
			attempted.Add(t.Id)
			if locked, lockErr := s.locker.TryLock(ctx, &t.Id, owner.ID()); !locked {
				// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
				if errors.Is(lockErr, token.SelectorRateLimited) {
					return nil, nil, immediateRetries, lockErr
				}
				if errors.Is(lockErr, driver.ErrTokenAlreadyLocked) {
					// Lost the race: someone else holds this token. This is the
					// expected, common case under contention, not a DB error.
					s.metrics.LockConflicts.Add(1)
					s.logger.DebugfContext(ctx, "Lost lock race on token [%s:%d]: already locked by another process", t.Id.TxId, t.Id.Index)
				} else {
					// A real store error (not a lock conflict) collapsed into the
					// same !locked branch by TryLock. Only the log line separates
					// the two: to the caller this still reads as ordinary
					// contention, so a store outage surfaces as locked funds
					// rather than as an error. See #2395.
					s.logger.WarnfContext(ctx, "Failed to lock token [%s:%d]: %v", t.Id.TxId, t.Id.Index, lockErr)
				}
				tokensLockedByOthersExist = true

				continue
			}
			s.logger.DebugfContext(ctx, "Got the lock on token [%v]", t)
			q, err := token2.ToQuantity(t.Quantity, s.precision)
			if err != nil {
				return nil, nil, immediateRetries, errors.Wrapf(err, "invalid token [%s] found", t.Id)
			}
			s.logger.DebugfContext(ctx, "Found token [%s] to add: [%s:%s].", t.Id, q.Decimal(), t.Type)
			immediateRetries = 0
			sum, err = sum.Add(q)
			if err != nil {
				return nil, nil, immediateRetries, errors.Wrapf(err, "failed to add quantity")
			}
			selected.Add(&t.Id)
			if sum.Cmp(quantity) >= 0 {
				return selected.ToSlice(), sum, immediateRetries, nil
			}
		}
	}
}

// trySingleTokenExactMatch runs the k=1 change-avoidance pre-search: it looks for a
// single candidate whose amount equals quantity and, if one is found and can be locked,
// returns it as a complete, change-free selection (ok=true). It reports ok=false — leaving
// the greedy walk to run — when no exact candidate exists, every exact candidate is
// currently locked by another process, or any error occurs. It is strictly best-effort: it
// never fails the selection and never holds a lock unless it returns that lock as the
// winning result.
//
// Every token the pre-search tries to lock is recorded in attempted, the same per-Select()
// set the greedy walk fills, so a change-free hit is still counted by
// observeDistinctTokensAttempted and every lost lock race increments LockConflicts exactly
// as in the greedy walk. A failure that is not a lock conflict (a real store error that
// TryLock collapses into the same !locked result) is logged at warn, not hidden at debug.
//
// The snapshot is read from a fresh iterator in a single linear pass that collects the
// exact-amount matches directly: there is no sort, so the pass is O(n), the same order the
// greedy walk already pays. That is why the feature stays enabled for the large, dust-heavy
// wallets it most helps instead of bailing above a candidate cap. When several candidates
// share the exact amount, the matches are shuffled before locking to spread contention
// across them (cf. the bucket shuffle in #2399). On a miss the snapshot is handed to the
// greedy walk via swapCache so it reuses it instead of fetching the wallet a second time; a
// fetch or iterator error bails early and leaves s.cache untouched so the greedy walk fetches
// for itself.
//
// The speculative locks it takes do not consume extra rate-limit budget under the built-in
// limiter, which meters one unit per Select() call (see ratelimit.Decorate), not per lock. A
// custom Locker that meters per lock would see these attempts, so the pre-search treats a
// SelectorRateLimited denial as a hard stop and never retries past it.
//
// It is called once per selectInternal invocation; under StubbornSelector selectInternal is
// re-entered on every backoff retry, so this pre-search runs once per backoff retry too.
func (s *Selector) trySingleTokenExactMatch(ctx context.Context, owner token.OwnerFilter, quantity token2.Quantity, tokenType token2.Type, attempted collections.Set[token2.ID]) ([]*token2.ID, token2.Quantity, bool) {
	it, err := s.fetcher.UnspentTokensIteratorBy(ctx, owner.ID(), tokenType)
	if err != nil {
		s.logger.DebugfContext(ctx, "exact-match pre-search: failed to fetch candidates: %v", err)

		return nil, nil, false
	}
	defer it.Close()

	// rawTokens keeps the full snapshot in fetch order so that, on a miss, it can be handed to
	// the greedy walk instead of being discarded and re-fetched. It includes tokens with a
	// malformed amount, which the pre-search skips but the greedy walk must still see so the
	// error surfaces consistently. matches holds just the ids whose amount equals the request.
	rawTokens := make([]*token2.UnspentTokenInWallet, 0)
	matches := make([]token2.ID, 0)
	for {
		t, err := it.Next()
		if err != nil {
			s.logger.DebugfContext(ctx, "exact-match pre-search: iterator error: %v", err)

			return nil, nil, false
		}
		if t == nil {
			break
		}
		rawTokens = append(rawTokens, t)
		amount, err := token2.ToQuantity(t.Quantity, s.precision)
		if err != nil {
			// A malformed amount is left for the greedy path to surface consistently.
			s.logger.DebugfContext(ctx, "exact-match pre-search: skipping token [%s] with invalid amount: %v", t.Id, err)

			continue
		}
		if amount.Cmp(quantity) == 0 {
			matches = append(matches, t.Id)
		}
	}

	// seedGreedy hands the already-materialised snapshot to the greedy walk so a miss does not
	// trigger a second full fetch of the same wallet. It is only called once the whole iterator
	// has been read; the early return above bails before the snapshot is complete.
	seedGreedy := func() {
		if len(rawTokens) == 0 {
			return
		}
		if err := s.swapCache(iterators.Slice(rawTokens)); err != nil {
			s.logger.DebugfContext(ctx, "exact-match pre-search: could not seed greedy cache: %v", err)
		}
	}

	if len(matches) == 0 {
		seedGreedy()

		return nil, nil, false
	}

	// Shuffle the equal-amount matches so concurrent selectors do not all contend for the same
	// exact-amount token first.
	rand.Shuffle(len(matches), func(i, j int) { matches[i], matches[j] = matches[j], matches[i] })

	for i := range matches {
		// Bail if a concurrent Manager.Close() removed this selector while we were scanning, so
		// the pre-search does not acquire a lock for a selector that is already gone (the greedy
		// walk makes the same check inside s.next()). The window between this check and TryLock
		// matches the greedy walk's; a lock slipped into it is reclaimed by the lease cleaner.
		if s.isClosed() {
			return nil, nil, false
		}
		id := matches[i]
		// Count the attempt before the outcome is known, so the per-Select() distinct-token
		// fan-out counts the pre-search like the greedy walk.
		attempted.Add(id)
		locked, lockErr := s.locker.TryLock(ctx, &id, owner.ID())
		if locked {
			// amount == quantity for every match, so the request is covered exactly, no change.
			return []*token2.ID{&id}, quantity, true
		}
		if errors.Is(lockErr, token.SelectorRateLimited) {
			// A rate-limit denial is a hard stop: the greedy walk will hit the same limit and
			// surface it consistently. Do not seed; the request is aborting anyway.
			return nil, nil, false
		}
		if errors.Is(lockErr, driver.ErrTokenAlreadyLocked) {
			// Lost the race: someone else holds this token. Expected contention, counted the
			// same way as in the greedy walk, not a store error.
			s.metrics.LockConflicts.Add(1)
			s.logger.DebugfContext(ctx, "exact-match pre-search: candidate [%s] already locked by another process, trying next", id)
		} else {
			// A real store error collapsed into the same !locked branch by TryLock. Surface it
			// at warn, as the greedy walk does, rather than hiding an outage at debug. See #2395.
			s.logger.WarnfContext(ctx, "exact-match pre-search: failed to lock candidate [%s]: %v", id, lockErr)
		}
	}

	// Every exact candidate is held by another process: fall through to the greedy walk, which
	// reuses the snapshot above rather than re-fetching.
	seedGreedy()

	return nil, nil, false
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

func NewSherdSelector(txID transaction.ID, fetcher TokenFetcher, lockDB Locker, precision uint64, backoff time.Duration, maxRetriesAfterBackoff int, m *Metrics, opts ...Option) TokenSelectorUnlocker {
	logger := logger.Named("selector-" + txID)
	locker := &locker{txID: txID, Locker: lockDB}
	if backoff < 0 {
		return NewSelector(logger, fetcher, locker, precision, m, opts...)
	} else {
		return NewStubbornSelector(logger, fetcher, locker, precision, backoff, maxRetriesAfterBackoff, m, opts...)
	}
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"fmt"
	"math/big"
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
)

const (
	// This way we avoid deadlocks, e.g. We have 2 tokens of value 10CHF each (20 CHF in total).
	// We also have two processes that both ask for 15CHF. If both of them concurrently lock one token each,
	// they will retry maxRetry times to see if the other process in the meantime unlocked the token.
	// If not, to avoid locking these tokens forever, we roll back and unlock the tokens.
	maxImmediateRetries = 5
	NoBackoff           = -1

	// sufficiencyWindow bounds how many ascending-by-amount candidates
	// nextCandidate considers together once it finds one that, on its own,
	// already covers the remaining requested amount. bucketedIterator's
	// shuffle (fetcher.go) only randomizes tokens whose Quantity is
	// byte-equal, so with realistic wallets (mostly-distinct amounts, as in
	// the #2395 CERT incident) every such bucket has size 1 and the
	// ascending scan is fully deterministic: any request smaller than the
	// smallest token always targets that single token, exactly the hot-spot
	// #2395 warned against. sufficiencyWindow widens the randomization to
	// "all individually-sufficient candidates within a bounded lookahead",
	// not just byte-equal ones. The size is a tradeoff: too small (1) is the
	// old fully-deterministic behavior; too large risks locking a much
	// bigger token than needed for a small payment - its own complaint in
	// #2395 ("a 1 CHF request grabbed a 200 CHF token"). 4 was chosen as a
	// small constant that still gives real statistical spread across a
	// handful of similarly-sized candidates while keeping the selected
	// token close to the smallest sufficient one.
	sufficiencyWindow = 4

	// maxSufficiencyRatio additionally bounds the sufficiency window by magnitude, not just
	// count: a candidate only joins the window if its quantity is at most maxSufficiencyRatio
	// times the *anchor's* quantity - the anchor being the first individually-sufficient
	// candidate, i.e. the smallest token the ascending scan would have locked anyway. Count
	// alone is not enough - a wallet with very few distinct amounts (e.g. exactly one small
	// token and one huge one, as in TestSizeOrderedSelection_SmallestFit) would otherwise have
	// sufficiencyWindow trivially swallow the huge token just because nothing else was in
	// between, defeating the smallest-fit bias for the smallest wallets, which are also the
	// ones a hot-token incident hurts the most. 5x keeps the window meaningful (room for
	// several genuinely similarly-sized candidates around the CERT incident's 1/1.5/2/3 EUR
	// cluster) while still refusing to lock, say, a 200 EUR token when a 1 EUR one would do.
	//
	// The bound is deliberately anchor-relative and not remaining-relative: with the remaining
	// amount as the basis, any wallet whose smallest token already exceeds 5x the request (a
	// 1 EUR payment out of 20/30/40/50 EUR denominations - entirely ordinary, and the #2395
	// CERT incident's own shape) would see its very first lookahead candidate rejected, leaving
	// a window of size 1 and fully deterministic selection: the mechanism would be inert in
	// exactly the regime it exists for. Anchoring on the smallest sufficient token keeps the
	// selected token within 5x of what would have been locked regardless, in every regime.
	maxSufficiencyRatio = 5
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
	// pending holds candidates peeked by nextCandidate's sufficiency-window
	// lookahead but not chosen, in their original ascending relative order,
	// so a later call still sees them before any newer cache/refetch result.
	// Only selectInternal's single goroutine touches it, so it needs no lock.
	pending []*token2.UnspentTokenInWallet
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
	for retriesAfterBackoff := 0; retriesAfterBackoff <= m.maxRetriesAfterBackoff; retriesAfterBackoff++ {
		if tokens, quantity, err := m.selectWithoutMetrics(ctx, ownerFilter, q, tokenType); err == nil || !errors.Is(err, token.SelectorSufficientButLockedFunds) {
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

func (s *Selector) selectInternal(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, int, error) {
	if s.isClosed() {
		return nil, nil, 0, errors.Errorf("selector is already closed")
	}
	quantity, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, 0, errors.Wrapf(err, "failed to create quantity")
	}
	sum, selected, tokensLockedByOthersExist, immediateRetries := token2.NewZeroQuantity(s.precision), collections.NewSet[*token2.ID](), true, 0
	// attempted tracks every distinct token this call has tried a lock on, so we
	// can report DistinctTokensAttempted at the end. A token counts as attempted
	// whatever the outcome - lock won, lost to a conflict, or denied by the rate
	// limiter - because TryLock was called on it in every one of those cases.
	attempted := collections.NewSet[token2.ID]()
	defer func() {
		s.metrics.DistinctTokensAttempted.Observe(float64(attempted.Length()))
	}()
	// blacklisted holds tokens this call has already lost a lock race on, so a
	// refetch does not immediately re-attempt (and re-lose) the same race
	// against the same hot token: see #2395, where one token was re-proposed
	// in a loop for over six minutes. It is scoped to this single Select
	// call, not process-global, so a token that is genuinely freed by
	// another process is reconsidered on the caller's next Select call.
	blacklisted := collections.NewSet[token2.ID]()
	// sawNonBlacklistedCandidate tracks whether the current scan of the
	// cache (since the last refetch) produced at least one candidate that
	// was not already blacklisted. If a whole scan sees nothing but
	// blacklisted tokens, the blacklist is excluding every candidate we
	// have, so it is cleared below: otherwise a genuinely-contended wallet
	// with no other tokens would turn a lost race into a permanent false
	// insufficient-funds instead of ever retrying.
	sawNonBlacklistedCandidate := false
	// batchLocker is non-nil when the underlying store can claim several candidates in one
	// round trip (see BatchLocker). When present, the loop below claims a covering window
	// of candidates per attempt instead of one token at a time, which is what actually
	// dissolves the ordering hot spot from #2395: reading it once up front means the
	// decision is made per Select call, not per iteration.
	batchLocker, supportsBatch := s.locker.(BatchTokenLocker)
	for {
		remaining, remainingErr := quantity.Sub(sum)
		if remainingErr != nil {
			return nil, nil, immediateRetries, errors.Wrapf(remainingErr, "failed to compute remaining amount for [%s:%s]", owner.ID(), tokenType)
		}
		if t, err := s.nextCandidate(remaining); err != nil {
			return nil, nil, immediateRetries, errors.Wrapf(err, "failed to get tokens for [%s:%s]", owner.ID(), tokenType)
		} else if t == nil {
			if !tokensLockedByOthersExist {
				// The candidate query excludes already-locked tokens (#2395,
				// mechanism 3), so an empty scan that never saw a lock conflict is
				// ambiguous: it may mean this wallet truly has no more funds, or
				// that every remaining token is currently locked by someone else
				// and was hidden from us entirely. Disambiguate with a direct,
				// lock-ignoring existence check before giving up.
				// HasEnoughSpendableTokens sums the wallet's whole spendable balance: a wallet
				// that cannot cover the request can never satisfy this Select call no matter how
				// the rest gets unlocked, so fail immediately instead of spending the
				// immediate-retry/backoff budget on a request that can never succeed.
				//
				// The comparison is against the full requested quantity, not the remaining
				// amount: the query deliberately ignores locks, so the total it reports still
				// includes the tokens this very call has already locked and counted into sum.
				// Comparing against remaining (= quantity - sum) would put those tokens on both
				// sides, degenerating into `total >= total - sum` — true as soon as anything at
				// all was selected, which is precisely when the fast fail is needed. Since the
				// total already includes sum, `total >= quantity` is the equivalent,
				// non-double-counting form of `total - sum >= remaining`.
				hasEnough, hasEnoughErr := s.fetcher.HasEnoughSpendableTokens(ctx, owner.ID(), tokenType, quantity.ToBigInt())
				if hasEnoughErr != nil {
					return nil, nil, immediateRetries, errors.Wrapf(hasEnoughErr, "failed to check for locked tokens for [%s:%s]", owner.ID(), tokenType)
				}
				if !hasEnough {
					return nil, nil, immediateRetries, errors.Wrapf(
						token.SelectorInsufficientFunds,
						"insufficient funds, only [%s] tokens of type [%s] are available, but [%s] were requested and no other process has any tokens locked",
						sum.Decimal(),
						tokenType,
						quantity.Decimal(),
					)
				}
			}

			if !sawNonBlacklistedCandidate && !blacklisted.Empty() {
				s.logger.DebugfContext(ctx, "Blacklist excluded every candidate this scan; clearing it so freed tokens can be retried.")
				blacklisted = collections.NewSet[token2.ID]()
			}
			sawNonBlacklistedCandidate = false

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
		} else if blacklisted.Contains(t.Id) {
			// Already lost the race on this token earlier in this same
			// Select call: don't re-attempt it, just note that a locked
			// token exists so the caller keeps retrying/backing off instead
			// of reporting insufficient funds.
			s.logger.DebugfContext(ctx, "Skipping blacklisted token [%v]: already lost a lock race on it this call", t.Id)
			tokensLockedByOthersExist = true
		} else if supportsBatch {
			// Grow the window from t until it covers the remaining amount (or the cache
			// runs out), then claim the whole window in one call. This never claims more
			// than the minimal covering prefix, so no won-but-unselected token is ever
			// left locked: every token claimed here either ends up in selected below, or
			// was never actually locked in the first place (a lost race).
			window := []*token2.UnspentTokenInWallet{t}
			windowSum, err := token2.ToQuantity(t.Quantity, s.precision)
			if err != nil {
				return nil, nil, immediateRetries, errors.Wrapf(err, "invalid token [%s] found", t.Id)
			}
			for windowSum.Cmp(remaining) < 0 {
				next, nextErr := s.dequeue()
				if nextErr != nil {
					return nil, nil, immediateRetries, errors.Wrapf(nextErr, "failed to get tokens for [%s:%s]", owner.ID(), tokenType)
				}
				if next == nil {
					break
				}
				if blacklisted.Contains(next.Id) {
					continue
				}
				nq, err := token2.ToQuantity(next.Quantity, s.precision)
				if err != nil {
					return nil, nil, immediateRetries, errors.Wrapf(err, "invalid token [%s] found", next.Id)
				}
				windowSum, err = windowSum.Add(nq)
				if err != nil {
					return nil, nil, immediateRetries, errors.Wrapf(err, "failed to add quantity")
				}
				window = append(window, next)
			}

			ids := make([]*token2.ID, len(window))
			for i := range window {
				ids[i] = &window[i].Id
			}
			won, lockErr := batchLocker.TryLockBatch(ctx, ids, owner.ID())
			if lockErr != nil {
				// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
				if errors.Is(lockErr, token.SelectorRateLimited) {
					return nil, nil, immediateRetries, lockErr
				}
				// A real store error (not per-token contention) failed the whole batch.
				// Don't blacklist: none of these tokens are known to be lost races.
				s.logger.Warnf("Failed to batch-lock %d token(s): %v", len(window), lockErr)
				for _, wt := range window {
					attempted.Add(wt.Id)
				}
				sawNonBlacklistedCandidate = true
				tokensLockedByOthersExist = true

				continue
			}
			wonSet := collections.NewSet[token2.ID]()
			for _, id := range won {
				wonSet.Add(*id)
			}
			for _, wt := range window {
				attempted.Add(wt.Id)
				sawNonBlacklistedCandidate = true
				if !wonSet.Contains(wt.Id) {
					s.metrics.LockConflicts.Add(1)
					s.logger.Infof("Lost lock race on token [%s:%d]: already locked by another process", wt.Id.TxId, wt.Id.Index)
					blacklisted.Add(wt.Id)
					tokensLockedByOthersExist = true

					continue
				}
				s.logger.DebugfContext(ctx, "Got the lock on token [%v]", wt)
				q, err := token2.ToQuantity(wt.Quantity, s.precision)
				if err != nil {
					return nil, nil, immediateRetries, errors.Wrapf(err, "invalid token [%s] found", wt.Id)
				}
				immediateRetries = 0
				sum, err = sum.Add(q)
				if err != nil {
					return nil, nil, immediateRetries, errors.Wrapf(err, "failed to add quantity")
				}
				selected.Add(&wt.Id)
			}
			if sum.Cmp(quantity) >= 0 {
				return selected.ToSlice(), sum, immediateRetries, nil
			}
		} else {
			// Counted once here, before the outcome is known, so a later third
			// outcome branch cannot forget to record the attempt.
			attempted.Add(t.Id)
			sawNonBlacklistedCandidate = true
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
					blacklisted.Add(t.Id)
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

// dequeue returns the next candidate, preferring anything already peeked and
// buffered by a previous nextCandidate call (in its original relative
// order) over pulling a fresh one from the cache.
func (s *Selector) dequeue() (*token2.UnspentTokenInWallet, error) {
	if len(s.pending) > 0 {
		t := s.pending[0]
		s.pending = s.pending[1:]

		return t, nil
	}

	return s.next()
}

// nextCandidate is the sufficiency-window-aware replacement for a plain
// s.next() call: it returns the next candidate to consider for satisfying
// remaining, but when that candidate already covers remaining on its own,
// it does not always return the very first such candidate. Because the
// cache yields tokens in ascending-by-amount order (bucketedIterator,
// fetcher.go), every subsequent candidate from here on is >= this one and
// therefore also individually sufficient, so it peeks up to
// sufficiencyWindow of them - stopping early at the first one whose
// quantity exceeds maxSufficiencyRatio times that first candidate's own
// quantity (see maxSufficiencyRatio) - and returns one
// chosen uniformly at random, buffering the rest via s.pending so they are
// still considered, in order, on later calls. This is what spreads "small
// payment locks the single smallest token" contention across several
// similarly-sized tokens (#2395) for wallets with mostly-distinct amounts,
// where bucketedIterator's byte-equal-only shuffle has nothing to shuffle.
// When the candidate alone does not cover remaining, it is returned
// immediately with no lookahead: satisfying remaining will require
// combining multiple tokens regardless (handled by selectInternal's own
// batch-window-growing loop), so widening the window here would only
// needlessly consume more of the cache.
func (s *Selector) nextCandidate(remaining token2.Quantity) (*token2.UnspentTokenInWallet, error) {
	t, err := s.dequeue()
	if err != nil || t == nil {
		return t, err
	}

	tq, err := token2.ToQuantity(t.Quantity, s.precision)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid token [%s] found", t.Id)
	}
	if tq.Cmp(remaining) < 0 {
		return t, nil
	}

	threshold := new(big.Int).Mul(tq.ToBigInt(), big.NewInt(maxSufficiencyRatio))

	window := []*token2.UnspentTokenInWallet{t}
	for len(window) < sufficiencyWindow {
		next, nextErr := s.dequeue()
		if nextErr != nil {
			return nil, nextErr
		}
		if next == nil {
			break
		}
		nq, nqErr := token2.ToQuantity(next.Quantity, s.precision)
		if nqErr != nil {
			return nil, errors.Wrapf(nqErr, "invalid token [%s] found", next.Id)
		}
		if nq.ToBigInt().Cmp(threshold) > 0 {
			// Too much bigger than the anchor, i.e. than the token this call would
			// have locked anyway: put it back (ascending order means every candidate
			// from here on is >= this one, hence also over threshold, so there is no
			// point looking further).
			s.pending = append([]*token2.UnspentTokenInWallet{next}, s.pending...)

			break
		}
		window = append(window, next)
	}

	idx := 0
	if len(window) > 1 {
		idx = rand.IntN(len(window))
	}
	chosen := window[idx]

	rest := make([]*token2.UnspentTokenInWallet, 0, len(window)-1)
	for i, w := range window {
		if i != idx {
			rest = append(rest, w)
		}
	}
	s.pending = append(rest, s.pending...)

	return chosen, nil
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

// batchLocker adds TryLockBatch to locker, forwarding to a BatchLocker bound to the same
// consumer transaction. It is constructed only when the underlying raw Locker actually
// implements BatchLocker (see NewSherdSelector), so a s.locker.(BatchTokenLocker) assertion
// in selectInternal reflects genuine backend capability, not just this adapter's shape.
type batchLocker struct {
	*locker
	batch BatchLocker
}

func (l *batchLocker) TryLockBatch(ctx context.Context, tokenIDs []*token2.ID, walletID string) ([]*token2.ID, error) {
	won, err := l.batch.LockBatch(ctx, tokenIDs, l.txID, walletID)
	if err != nil {
		logger.DebugfContext(ctx, "failed to batch-lock %d token(s) for [%s]: [%s]", len(tokenIDs), l.txID, err)
	}

	return won, err
}

func NewSherdSelector(txID transaction.ID, fetcher TokenFetcher, lockDB Locker, precision uint64, backoff time.Duration, maxRetriesAfterBackoff int, m *Metrics) TokenSelectorUnlocker {
	logger := logger.Named("selector-" + txID)
	base := &locker{txID: txID, Locker: lockDB}
	var tokenLocker TokenLocker = base
	if bl, ok := lockDB.(BatchLocker); ok {
		tokenLocker = &batchLocker{locker: base, batch: bl}
	}
	if backoff < 0 {
		return NewSelector(logger, fetcher, tokenLocker, precision, m)
	} else {
		return NewStubbornSelector(logger, fetcher, tokenLocker, precision, backoff, maxRetriesAfterBackoff, m)
	}
}

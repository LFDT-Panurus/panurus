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

// NoBackoff tells NewSherdSelector to build a plain Selector rather than a StubbornSelector:
// no backoff-and-retry cycle wraps selection, so a single losing attempt is final.
const NoBackoff = -1

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
	// tuning is normalized once, at construction (see NewSelector), and never mutated
	// afterwards: every invariant below assumes a Tuning that cannot change over the life of
	// this Selector.
	tuning Tuning
	mu     sync.Mutex // protects the cache and pending fields
	// pending holds candidates peeked by nextCandidate's sufficiency-window
	// lookahead but not chosen, in their original ascending relative order,
	// so a later call still sees them before any newer cache/refetch result.
	//
	// It is guarded by mu, like cache: a selection is expected to be driven by a
	// single goroutine, but the manager hands out one cached Selector per
	// transaction.ID (manager.go) and Close can be called from another goroutine
	// at any time, so the buffer is read and written only through dequeue and
	// requeue, and cleared by swapCache and Close, all of which hold mu.
	//
	// Its length stays below tuning.SufficiencyWindow at all times, including in a wallet
	// where every token is individually sufficient and therefore every call grows a
	// full window: nextCandidate dequeues from pending before it touches the cache,
	// so a window is assembled out of pending first and only at most
	// tuning.SufficiencyWindow-1 of those entries are ever buffered back. The buffer is
	// bounded by the window size, not by the wallet or the cache size, and
	// refreshCandidates drops it outright whenever it installs a fresh cache.
	pending []*token2.UnspentTokenInWallet
}

type StubbornSelector struct {
	*Selector
	// After Tuning.MaxImmediateRetries attempts, the procs will roll back and unlock the tokens.
	// If two procs unlock at the same time, we have a livelock.
	// To avoid it, we back off (wait) for a random interval within some limits and retry
	backoffInterval time.Duration
	// However, it might be that we don't have a livelock, but we are simply out of funds.
	// Instead of polling forever, we can abort after a certain amount of attempts.
	maxRetriesAfterBackoff int
}

// Select retries selectWithoutMetrics across backoff cycles until it succeeds, gives up for
// good, or the context is cancelled. Each inner attempt runs its own, independent
// selectInternal call with a fresh cache, so ImmediateRetries and DistinctTokensAttempted
// are aggregated over every inner attempt here and observed exactly once - rather than once
// per inner attempt: a caller watching these metrics wants the cost of the whole outer Select
// call, not just its last inner leg. The two aggregate differently. DistinctTokensAttempted
// counts distinct tokens, so the legs share one set and a token contended across several of
// them counts once; ImmediateRetries counts events, so the per-leg counts are summed on
// whichever of the three exit paths below is taken.
func (m *StubbornSelector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	start := time.Now()
	// One set for the whole call: each backoff round runs a fresh inner selection, but
	// the histogram reports per-Select() fan-out, so the distinct tokens seen across
	// every round are unioned here and observed exactly once.
	attempted := collections.NewSet[token2.ID]()
	defer observeDistinctTokensAttempted(m.metrics, attempted)
	// ImmediateRetries has no such set to union into, so it is accumulated explicitly:
	// each inner leg reports its own count, and the caller wants the cost of the whole
	// outer Select call rather than just that of its last leg.
	totalImmediateRetries := 0
	for retriesAfterBackoff := 0; retriesAfterBackoff <= m.maxRetriesAfterBackoff; retriesAfterBackoff++ {
		if err := ctx.Err(); err != nil {
			return m.aborted(ctx, start, totalImmediateRetries, err)
		}
		tokens, quantity, immediateRetries, err := m.selectWithoutMetrics(ctx, ownerFilter, q, tokenType, attempted)
		totalImmediateRetries += immediateRetries
		// A caller that gave up during this leg gets ctx.Err(), not a sentinel describing funds
		// it no longer cares about. This also covers the backoff below: a zero-length timer
		// and an already-done context are both ready at once, and the select would pick either.
		if err != nil && ctx.Err() != nil {
			return m.aborted(ctx, start, totalImmediateRetries, ctx.Err())
		}
		if err == nil || !errors.Is(err, token.SelectorSufficientButLockedFunds) {
			m.observeSelection(start, totalImmediateRetries, selectionOutcome(err))

			return tokens, quantity, err
		}
		if err := m.backoff(ctx); err != nil {
			return m.aborted(ctx, start, totalImmediateRetries, err)
		}
		m.logger.DebugfContext(ctx, "Now it is our turn to retry...")
	}

	m.observeSelection(start, totalImmediateRetries, "locked_funds")

	return nil, nil, errors.Wrapf(token.SelectorInsufficientFunds, "aborted too many times and no other process unlocked or added tokens")
}

// aborted ends the call for a caller whose context is done. Every inner leg releases its
// locks before returning an error, but UnlockAll is idempotent, so it is called again here
// rather than relying on which path noticed the cancellation.
func (m *StubbornSelector) aborted(ctx context.Context, start time.Time, immediateRetries int, cause error) ([]*token2.ID, token2.Quantity, error) {
	if err := m.locker.UnlockAll(ctx); err != nil {
		m.logger.Errorf("failed to unlock tokens on context cancellation: %s", err)
	}
	m.observeSelection(start, immediateRetries, "error")

	return nil, nil, errors.Wrap(cause, "token selection aborted")
}

// backoff waits a random interval of up to backoffInterval, so that processes that gave up
// on the same tokens at the same time do not all retry at once. It returns the context's
// error if the context is done first.
func (m *StubbornSelector) backoff(ctx context.Context) error {
	var backoffDuration time.Duration
	if m.backoffInterval > 0 {
		backoffDuration = time.Duration(rand.Int64N(int64(m.backoffInterval)))
	}
	m.logger.DebugfContext(ctx,
		"Token selection aborted, so that other procs can retry. Release tokens and backoff for %v before retrying to select. "+
			"In the meantime maybe some other process releases token locks or adds tokens.",
		backoffDuration)
	backoffStart := time.Now()
	timer := time.NewTimer(backoffDuration)
	defer timer.Stop()
	select {
	case <-timer.C:
		m.metrics.BackoffDuration.Observe(time.Since(backoffStart).Seconds())

		return nil
	case <-ctx.Done():
		m.metrics.BackoffDuration.Observe(time.Since(backoffStart).Seconds())

		return ctx.Err()
	}
}

func NewStubbornSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, backoff time.Duration, retries int, m *Metrics, opts ...Option) *StubbornSelector {
	return &StubbornSelector{
		Selector:               NewSelector(logger, tokenDB, lockDB, precision, m, opts...),
		backoffInterval:        backoff,
		maxRetriesAfterBackoff: retries,
	}
}

// NewSelector returns a Selector tuned by opts, falling back to the package defaults for
// anything opts leaves unset (see Tuning and CompileOptions).
func NewSelector(logger logging.Logger, tokenDB TokenFetcher, lockDB TokenLocker, precision uint64, m *Metrics, opts ...Option) *Selector {
	return &Selector{
		logger:    logger,
		cache:     collections.NewEmptyIterator[*token2.UnspentTokenInWallet](),
		fetcher:   tokenDB,
		locker:    lockDB,
		precision: precision,
		metrics:   m,
		tuning:    CompileOptions(opts...),
	}
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
	s.observeSelection(start, immediateRetries, selectionOutcome(err))

	return ids, quantity, err
}

// observeSelection records the duration, the immediate retries and the outcome of one
// Select call.
func (s *Selector) observeSelection(start time.Time, immediateRetries int, outcome string) {
	s.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
	s.metrics.ImmediateRetries.Observe(float64(immediateRetries))
	s.metrics.SelectionOutcome.With(outcomeLabel, outcome).Add(1)
}

// selectionOutcome is the SelectionOutcome label for a Select call that ended with err.
func selectionOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, token.SelectorSufficientButLockedFunds):
		return "locked_funds"
	case errors.Is(err, token.SelectorInsufficientFunds):
		return "insufficient_funds"
	default:
		return "error"
	}
}

// selectWithoutMetrics is used by StubbornSelector to avoid double-counting metrics: it
// returns the per-call immediateRetries so the caller can accumulate them across backoff
// retries and observe once, instead of observing them here per inner attempt. The attempted
// set is the caller's, and is unioned across those retries for the same reason.
func (s *Selector) selectWithoutMetrics(ctx context.Context, owner token.OwnerFilter, q string, tokenType token2.Type, attempted collections.Set[token2.ID]) ([]*token2.ID, token2.Quantity, int, error) {
	ids, quantity, immediateRetries, err := s.selectInternal(ctx, owner, q, tokenType, attempted)
	if err != nil {
		if err2 := s.locker.UnlockAll(ctx); err2 != nil {
			s.logger.Warnf("failed to unlock tokens after selection error: %v", err2)
		}
	}

	return ids, quantity, immediateRetries, err
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
	// Drop anything nextCandidate's lookahead left buffered by an earlier call. refreshCandidates
	// already does this whenever it installs a fresh cache, but its budget-exceeded early return
	// happens before that, so the leg that gives up on token.SelectorSufficientButLockedFunds
	// hands the buffer on intact. A StubbornSelector then backs off precisely so the world can
	// change - other processes releasing their locks - and the next leg must re-examine it from a
	// fresh fetch rather than replay candidates peeked from the pre-backoff snapshot, bypassing
	// both the refetch and the fresh set's own randomized sufficiency window. Dropping them is
	// lossless: the cache they came from is still installed, so they are re-offered by the next
	// fetch once it exhausts. See #2395.
	s.dropPending()

	sel := s.newSelection(owner, tokenType, quantity, attempted)
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, sel.immediateRetries, errors.Wrap(err, "token selection aborted")
		}
		done, err := sel.step(ctx)
		if err != nil {
			return nil, nil, sel.immediateRetries, err
		}
		if done {
			return sel.selected.ToSlice(), sel.sum, sel.immediateRetries, nil
		}
	}
}

// refreshCandidates charges one unit of the immediate-retry budget and reinstalls a freshly
// fetched candidate cache, so candidates this scan has already consumed — because they lost a
// lock race, or because a store error failed the batch they were in — become visible again in
// case the situation has changed meanwhile. It returns the updated retry count.
//
// Once the budget is spent it reports token.SelectorSufficientButLockedFunds instead of
// refetching: when we loop over the tokens we check whether a token is already locked, and
// every time our token cache finishes having noted that one of the tokens we saw was used by
// someone, we retry the fetch in case that other process unlocked it meanwhile, without
// unlocking our own tokens yet. After some retries we give up, and the caller unlocks.
//
// Any candidates buffered by nextCandidate's lookahead are dropped by swapCache: they were
// peeked from the cache being replaced, so the fresh, fully ordered candidate set supersedes
// them.
//
// When this is not the call's first fetch (alreadyFetched), the candidate source is told it is
// behind the store before it is read. A refetch served from the very snapshot whose exhaustion
// provoked it is not a retry at all: the candidate query excludes locked tokens (#2395
// mechanism 3), so a token another process has since unlocked - the event this retry is
// waiting for - is not in that snapshot and cannot appear by re-reading it, and neither can
// one this call locked itself disappear from it. The eager and mixed strategies would
// otherwise hand back the identical set until their freshness interval expired, spending the
// whole immediate-retry budget to re-read it and failing with SelectorSufficientButLockedFunds
// on a wallet that can pay.
//
// The first fetch is deliberately exempt: a selection starts with an empty cache, so it always
// comes through here once, and nothing is known to be stale at that point. Forcing a refresh
// there would make every Select re-read the whole store and reduce the eager strategy to a
// lazy one with a full-table scan. The lazy strategy implements no CacheInvalidator and reads
// through regardless, so all of this costs it nothing either way.
func (s *Selector) refreshCandidates(ctx context.Context, walletID string, tokenType token2.Type, immediateRetries int, alreadyFetched bool) (int, error) {
	if immediateRetries > s.tuning.MaxImmediateRetries {
		s.logger.Warnf("Exceeded max number of immediate retries. Unlock tokens and abort...")

		return immediateRetries, token.SelectorSufficientButLockedFunds
	}

	s.logger.DebugfContext(ctx, "Fetch all non-deleted tokens from the DB and refresh the token cache.")
	if alreadyFetched {
		s.invalidateCandidateCache(ctx, "Candidate scan exhausted before the request was covered")
	}
	it, err := s.fetcher.UnspentTokensIteratorBy(ctx, walletID, tokenType)
	if err != nil {
		return immediateRetries, errors.Wrapf(err, "failed to reload tokens for retry %d [%s:%s]", immediateRetries, walletID, tokenType)
	}
	if err := s.swapCache(it); err != nil {
		return immediateRetries, err
	}

	return immediateRetries + 1, nil
}

// dequeue returns the next candidate, preferring anything already peeked and
// buffered by a previous nextCandidate call (in its original relative order)
// over pulling a fresh one from the cache.
//
// It holds s.mu for the whole call, so that a concurrent Close can neither swap
// the iterator out, or nil it, while it is being read, nor observe a half-updated
// pending buffer. The closed check is made first and under the same lock, so a
// buffered candidate is never handed out by a selector that has been closed. It
// reports an error if the selector has already been closed.
func (s *Selector) dequeue() (*token2.UnspentTokenInWallet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache == nil {
		return nil, errors.New("selector is already closed")
	}
	if len(s.pending) > 0 {
		t := s.pending[0]
		s.pending = s.pending[1:]

		return t, nil
	}

	return s.cache.Next()
}

// requeue puts tokens back at the front of the buffer, ahead of anything already
// there, so the ascending order dequeue hands out is preserved. It takes ownership
// of the slice it is given, which must therefore not be reused by the caller.
func (s *Selector) requeue(tokens []*token2.UnspentTokenInWallet) {
	if len(tokens) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pending = append(tokens, s.pending...)
}

// dropPending discards the lookahead buffer, for the callers that do not go
// through swapCache or Close.
func (s *Selector) dropPending() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pending = nil
}

// nextCandidate is the sufficiency-window-aware replacement for a plain
// s.next() call: it returns the next candidate to consider for satisfying
// remaining, but when that candidate already covers remaining on its own,
// it does not always return the very first such candidate. Because the
// cache yields tokens in ascending-by-amount order (bucketedIterator,
// fetcher.go), every subsequent candidate from here on is >= this one and
// therefore also individually sufficient, so it peeks up to
// s.tuning.SufficiencyWindow of them - stopping early at the first one whose
// quantity exceeds s.tuning.MaxSufficiencyRatio times that first candidate's
// own quantity - and returns one
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
//
// isBlacklisted reports whether this Select call has already lost a lock race on a
// token. Such tokens are kept out of the window entirely: including them would spend
// part of the randomized pick on candidates that cannot possibly be locked, which
// weakens the very contention spreading the window exists to provide, and a blacklisted
// pick then gets re-buffered into s.pending only to be skipped again - so a scan over a
// fully-blacklisted cache re-walks it once per candidate instead of once. A blacklisted
// candidate is therefore returned straight away without a lookahead (selectInternal's own
// blacklist branch stays the single owner of the resulting bookkeeping, notably
// tokensLockedByOthersExist) and dropped, not re-buffered, while growing a window. Dropping
// mirrors what selectInternal's batch-window loop already does with them, and cannot turn a
// lost race into a false insufficient-funds: a window is only ever grown around a
// non-blacklisted anchor, whose own lock outcome drives those flags.
func (s *Selector) nextCandidate(remaining token2.Quantity, isBlacklisted func(token2.ID) bool) (*token2.UnspentTokenInWallet, error) {
	t, err := s.dequeue()
	if err != nil || t == nil {
		return t, err
	}
	if isBlacklisted(t.Id) {
		return t, nil
	}

	tq, err := token2.ToQuantity(t.Quantity, s.precision)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid token [%s] found", t.Id)
	}
	if tq.Cmp(remaining) < 0 {
		return t, nil
	}

	window, err := s.sufficiencyWindow(t, tq, isBlacklisted)
	if err != nil {
		return nil, err
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
	s.requeue(rest)

	return chosen, nil
}

// sufficiencyWindow returns t, which covers the remaining amount on its own with quantity
// tq, followed by up to Tuning.SufficiencyWindow-1 further non-blacklisted candidates no
// larger than Tuning.MaxSufficiencyRatio times tq. See nextCandidate.
func (s *Selector) sufficiencyWindow(t *token2.UnspentTokenInWallet, tq token2.Quantity, isBlacklisted func(token2.ID) bool) ([]*token2.UnspentTokenInWallet, error) {
	threshold := new(big.Int).Mul(tq.ToBigInt(), big.NewInt(int64(s.tuning.MaxSufficiencyRatio)))

	window := []*token2.UnspentTokenInWallet{t}
	for len(window) < s.tuning.SufficiencyWindow {
		next, nextErr := s.dequeue()
		if nextErr != nil {
			return nil, nextErr
		}
		if next == nil {
			break
		}
		if isBlacklisted(next.Id) {
			continue
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
			s.requeue([]*token2.UnspentTokenInWallet{next})

			break
		}
		window = append(window, next)
	}

	return window, nil
}

// swapCache installs it as the new token cache, drops the lookahead buffer that
// belonged to the old one, and closes the iterator it replaces, so a refresh on
// retry does not abandon a database cursor and its pooled connection. If the
// selector was closed in the meantime, it closes it too and reports an error: no
// iterator is ever left unclosed.
func (s *Selector) swapCache(it Iterator[*token2.UnspentTokenInWallet]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache == nil {
		it.Close()

		return errors.New("selector is already closed")
	}
	s.cache.Close()
	s.cache = it
	// The buffered candidates were peeked from the cache being replaced, so the fresh,
	// fully ordered candidate set supersedes them.
	s.pending = nil

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
	s.pending = nil

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

// invalidateCandidateCache tells the candidate source that its snapshot is behind the token
// store, when it has one to invalidate, and counts the forced refresh under
// Metrics.CacheInvalidations. reason names what proved the snapshot stale, for the log line.
//
// A fetcher that reads through implements no CacheInvalidator and needs no action, so a
// failed assertion is the normal case for the lazy strategy, not an error - and nothing is
// counted for it either, since no refresh is forced.
func (s *Selector) invalidateCandidateCache(ctx context.Context, reason string) {
	invalidator, ok := s.fetcher.(CacheInvalidator)
	if !ok {
		return
	}
	s.logger.DebugfContext(ctx, "%s; refreshing the token cache on the next read", reason)
	s.metrics.CacheInvalidations.Add(1)
	invalidator.InvalidateCache()
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

func (l *batchLocker) TryLockBatch(ctx context.Context, tokenIDs []*token2.ID, walletID string) (driver.BatchLockOutcome, error) {
	outcome, err := l.batch.LockBatch(ctx, tokenIDs, l.txID, walletID)
	if err != nil {
		logger.DebugfContext(ctx, "failed to batch-lock %d token(s) for [%s]: [%s]", len(tokenIDs), l.txID, err)
	}

	return outcome, err
}

func NewSherdSelector(txID transaction.ID, fetcher TokenFetcher, lockDB Locker, precision uint64, backoff time.Duration, maxRetriesAfterBackoff int, m *Metrics, opts ...Option) TokenSelectorUnlocker {
	logger := logger.Named("selector-" + txID)
	base := &locker{txID: txID, Locker: lockDB}
	var tokenLocker TokenLocker = base
	if bl, ok := asBatchLocker(lockDB); ok {
		tokenLocker = &batchLocker{locker: base, batch: bl}
	}
	if backoff < 0 {
		return NewSelector(logger, fetcher, tokenLocker, precision, m, opts...)
	} else {
		return NewStubbornSelector(logger, fetcher, tokenLocker, precision, backoff, maxRetriesAfterBackoff, m, opts...)
	}
}

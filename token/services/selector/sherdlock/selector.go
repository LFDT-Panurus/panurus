/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
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
	// maxScanCandidates bounds the exact-match pre-search. If a wallet+type has more
	// than this many unlocked candidates, the pre-search gives up and falls through to
	// the greedy walk rather than materialising and sorting an arbitrarily large slice
	// on every invocation. Matches the maxScanCandidates bound in the design doc.
	maxScanCandidates = 256
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
	// maxExactMatchInputs bounds the change-avoidance pre-search that runs before the
	// greedy walk: 0 disables it, 1 enables the k=1 single-completing-token search, and
	// 2 additionally enables the k=2 completing-pair search. Higher input counts are not
	// implemented and are treated as 2.
	maxExactMatchInputs int
}

// maxExactMatchPairs bounds how many distinct completing pairs the k=2 pre-search
// collects before shuffling and trying them, so a wallet holding many equal-value
// tokens cannot make the pair scan record an unbounded number of ties. It mirrors the
// bucket-shuffle anti-hotspot rationale of #2399: collect a handful, then pick one at
// random rather than always contending for the same pair first.
const maxExactMatchPairs = 4

// Option customizes a Selector at construction time.
type Option func(*Selector)

// WithExactMatch enables (or disables) the exact-amount change-avoidance pre-search at
// k=1 (a single completing token). It is off by default, preserving the plain greedy
// first-fit behaviour. To also enable the k=2 completing-pair search, use
// WithExactMatchInputs(2).
func WithExactMatch(enabled bool) Option {
	return func(s *Selector) {
		if enabled {
			s.maxExactMatchInputs = 1
		} else {
			s.maxExactMatchInputs = 0
		}
	}
}

// WithExactMatchInputs sets the maximum number of inputs the change-avoidance pre-search
// may combine to hit the request exactly: 0 disables it, 1 is the k=1 single-token search,
// and 2 additionally enables the k=2 completing-pair search. Values are clamped to [0, 2];
// k>2 is not implemented.
func WithExactMatchInputs(k int) Option {
	return func(s *Selector) {
		switch {
		case k < 0:
			s.maxExactMatchInputs = 0
		case k > 2:
			s.maxExactMatchInputs = 2
		default:
			s.maxExactMatchInputs = k
		}
	}
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

	// Change-avoidance pre-search: before the greedy walk commits any tokens, prefer an
	// unlocked combination of candidates whose amounts sum to the full request exactly,
	// which completes the selection with zero change. k=1 looks for a single completing
	// token; k=2 additionally looks for a completing pair. On any miss we fall through to
	// the greedy walk below, unchanged.
	if s.maxExactMatchInputs >= 1 {
		s.metrics.ExactMatchAttempts.Add(1)
		if ids, sum, ok := s.tryExactMatch(ctx, owner, quantity, tokenType); ok {
			s.metrics.ExactMatchHits.Add(1)
			s.logger.DebugfContext(ctx, "exact-match pre-search selected %d token(s) summing to [%s:%s] with no change", len(ids), quantity.Decimal(), tokenType)

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

// exactMatchCandidate is one unlocked spendable token considered by the pre-search,
// paired with its parsed amount so amounts are decoded once, not per comparison.
type exactMatchCandidate struct {
	id     token2.ID
	amount token2.Quantity
}

// tryExactMatch runs the change-avoidance pre-search over the wallet's current
// candidate set and returns a complete, change-free selection (ok=true) when one can be
// found and locked. It tries the cheapest completion first — a single token equal to the
// request (k=1) — and, only when maxExactMatchInputs >= 2 and no single token completes
// the request, a completing pair (k=2). It reports ok=false — leaving the greedy walk to
// run unchanged — on any miss, contention, or error.
//
// It is strictly best-effort: it never fails the selection, and it never leaves a lock
// held unless it returns that lock as part of the winning result (a partially locked pair
// is unwound, see lockExactMatchPair).
func (s *Selector) tryExactMatch(ctx context.Context, owner token.OwnerFilter, quantity token2.Quantity, tokenType token2.Type) ([]*token2.ID, token2.Quantity, bool) {
	candidates, ok := s.loadSortedCandidates(ctx, owner, tokenType)
	if !ok || len(candidates) == 0 {
		return nil, nil, false
	}

	// k=1: a single completing token is the ideal — one input, zero change — so it is
	// always tried first, even when k=2 is enabled.
	if ids, sum, ok := s.trySingleExactMatch(ctx, owner, quantity, candidates); ok {
		return ids, sum, true
	}

	// k=2: a completing pair. Only attempted when no single token completed the request.
	if s.maxExactMatchInputs >= 2 {
		if ids, sum, ok := s.tryPairExactMatch(ctx, owner, quantity, candidates); ok {
			s.metrics.ExactMatchPairHits.Add(1)

			return ids, sum, true
		}
	}

	return nil, nil, false
}

// loadSortedCandidates reads the wallet's current unlocked candidates for tokenType from a
// fresh iterator — so the greedy iterator (s.cache) is never disturbed — decodes their
// amounts, and returns them sorted ascending by amount. It returns ok=false on any fetch
// or iterator error, leaving the greedy walk to surface the failure consistently. Tokens
// with a malformed amount are skipped and left for the greedy path.
//
// loadSortedCandidates runs once per tryExactMatch call, and under StubbornSelector
// tryExactMatch is re-entered (with a fresh fetch) on every backoff retry. To keep that
// cost bounded, it gives up and returns ok=false — falling through to the greedy walk —
// once a wallet+type exposes more than maxScanCandidates unlocked candidates.
func (s *Selector) loadSortedCandidates(ctx context.Context, owner token.OwnerFilter, tokenType token2.Type) ([]exactMatchCandidate, bool) {
	it, err := s.fetcher.UnspentTokensIteratorBy(ctx, owner.ID(), tokenType)
	if err != nil {
		s.logger.DebugfContext(ctx, "exact-match pre-search: failed to fetch candidates: %v", err)

		return nil, false
	}
	defer it.Close()

	candidates := make([]exactMatchCandidate, 0)
	for {
		t, err := it.Next()
		if err != nil {
			s.logger.DebugfContext(ctx, "exact-match pre-search: iterator error: %v", err)

			return nil, false
		}
		if t == nil {
			break
		}
		amount, err := token2.ToQuantity(t.Quantity, s.precision)
		if err != nil {
			// A malformed amount is left for the greedy path to surface consistently.
			s.logger.DebugfContext(ctx, "exact-match pre-search: skipping token [%s] with invalid amount: %v", t.Id, err)

			continue
		}
		candidates = append(candidates, exactMatchCandidate{id: t.Id, amount: amount})
		if len(candidates) > maxScanCandidates {
			// Too many candidates to scan cheaply: give up and let the greedy walk
			// run unchanged. Bailing here (rather than after the loop) keeps the
			// materialised slice bounded and skips the sort entirely.
			s.logger.DebugfContext(ctx, "exact-match pre-search: candidate set exceeds cap of %d, falling back to greedy", maxScanCandidates)

			return nil, false
		}
	}

	// Sort ascending by amount so k=1 can binary-search and k=2 can two-pointer scan.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].amount.Cmp(candidates[j].amount) < 0 })

	return candidates, true
}

// trySingleExactMatch is the k=1 pre-search over the pre-sorted candidates: it
// binary-searches for a candidate whose amount equals quantity and, if one is found and
// lockable, returns it as a single-token, change-free selection. When several candidates
// share the exact amount, the tie is broken by shuffling those candidates' ids before
// locking, so concurrent selectors do not all contend for the same token first (cf. the
// bucket shuffle in #2399). It never mutates candidates, so the k=2 scan can still rely
// on the ascending order.
func (s *Selector) trySingleExactMatch(ctx context.Context, owner token.OwnerFilter, quantity token2.Quantity, candidates []exactMatchCandidate) ([]*token2.ID, token2.Quantity, bool) {
	lo := sort.Search(len(candidates), func(i int) bool { return candidates[i].amount.Cmp(quantity) >= 0 })
	if lo >= len(candidates) || candidates[lo].amount.Cmp(quantity) != 0 {
		return nil, nil, false
	}

	// Collect the ids of the contiguous run of candidates that equal quantity into a
	// local slice (leaving candidates sorted for the k=2 scan) and shuffle it.
	matches := make([]token2.ID, 0)
	for hi := lo; hi < len(candidates) && candidates[hi].amount.Cmp(quantity) == 0; hi++ {
		matches = append(matches, candidates[hi].id)
	}
	rand.Shuffle(len(matches), func(i, j int) { matches[i], matches[j] = matches[j], matches[i] })

	for _, id := range matches {
		locked, lockErr := s.locker.TryLock(ctx, &id, owner.ID())
		if errors.Is(lockErr, token.SelectorRateLimited) {
			// The greedy walk will hit the same rate limit and surface it consistently.
			return nil, nil, false
		}
		if locked {
			return []*token2.ID{&id}, quantity, true
		}
		s.logger.DebugfContext(ctx, "exact-match pre-search: single candidate [%s] already locked, trying next", id)
	}

	return nil, nil, false
}

// tryPairExactMatch is the k=2 pre-search over the pre-sorted candidates: a single
// two-pointer scan collects up to maxExactMatchPairs distinct pairs whose amounts sum to
// quantity exactly, the collected pairs are shuffled to spread contention, and each pair
// is then locked as a unit. The first fully lockable pair is returned as a two-input,
// change-free selection. A pair whose second token loses its lock race is unwound (see
// lockExactMatchPair), so on a miss no lock is left behind and the greedy walk starts
// clean.
func (s *Selector) tryPairExactMatch(ctx context.Context, owner token.OwnerFilter, quantity token2.Quantity, candidates []exactMatchCandidate) ([]*token2.ID, token2.Quantity, bool) {
	type pair struct{ a, b exactMatchCandidate }
	pairs := make([]pair, 0, maxExactMatchPairs)
	for lo, hi := 0, len(candidates)-1; lo < hi && len(pairs) < maxExactMatchPairs; {
		sum, err := candidates[lo].amount.Add(candidates[hi].amount)
		if err != nil {
			// Amounts are store-validated; a sum error is unexpected. Bail out of the
			// pair search and let the greedy walk proceed.
			s.logger.DebugfContext(ctx, "exact-match pre-search: pair sum error: %v", err)

			return nil, nil, false
		}
		switch sum.Cmp(quantity) {
		case 0:
			pairs = append(pairs, pair{a: candidates[lo], b: candidates[hi]})
			lo++
			hi--
		case -1:
			lo++
		default:
			hi--
		}
	}
	if len(pairs) == 0 {
		return nil, nil, false
	}
	rand.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })

	for _, p := range pairs {
		if ids, sum, ok := s.lockExactMatchPair(ctx, owner, p.a, p.b); ok {
			return ids, sum, true
		}
	}

	return nil, nil, false
}

// lockExactMatchPair locks both tokens of a completing pair. It locks the first, then the
// second; if the second cannot be acquired (contended or rate-limited) it releases the
// first before reporting failure, so the pre-search never leaves a half-locked pair
// behind. Because the pre-search runs before the greedy walk and before any other lock is
// taken for this selection, releasing via the locker's UnlockAll unwinds only this
// pre-search's own acquisition. On success it returns both ids and their sum.
func (s *Selector) lockExactMatchPair(ctx context.Context, owner token.OwnerFilter, a, b exactMatchCandidate) ([]*token2.ID, token2.Quantity, bool) {
	first := a.id
	locked, lockErr := s.locker.TryLock(ctx, &first, owner.ID())
	if errors.Is(lockErr, token.SelectorRateLimited) {
		return nil, nil, false
	}
	if !locked {
		// Nothing locked yet, so nothing to unwind; try the next pair.
		s.logger.DebugfContext(ctx, "exact-match pre-search: pair token [%s] already locked, skipping pair", first)

		return nil, nil, false
	}

	second := b.id
	// Whether the second lock is lost to plain contention or a rate-limit denial, the
	// response is the same: release the first token and let the greedy walk take over
	// (it re-surfaces any rate limit consistently), so the specific error is not needed.
	locked, _ = s.locker.TryLock(ctx, &second, owner.ID())
	if !locked {
		s.logger.DebugfContext(ctx, "exact-match pre-search: pair token [%s] already locked, releasing [%s] and skipping pair", second, first)
		s.releasePreSearchLocks(ctx)

		return nil, nil, false
	}

	sum, err := a.amount.Add(b.amount)
	if err != nil {
		s.logger.DebugfContext(ctx, "exact-match pre-search: pair sum error after locking: %v", err)
		s.releasePreSearchLocks(ctx)

		return nil, nil, false
	}

	return []*token2.ID{&first, &second}, sum, true
}

// releasePreSearchLocks unwinds any locks the exact-match pre-search has acquired so far
// (in practice, the first token of a pair whose second token was contended).
//
// It unlocks by consumer-tx id (UnlockAll -> UnlockByTxID), not by token id, so it releases
// every token this selection currently holds. That is correct only under an invariant that
// must be kept in mind before changing this code: each lockExactMatchPair attempt enters
// with a clean lock set, so the only lock held when this runs is the single first token of
// the current pair. That invariant holds because the pre-search runs before the greedy
// walk, k=1 leaves nothing locked on a miss, and the pairs are tried strictly sequentially
// with every failed attempt fully unwound (here) before the next begins — so N failed pairs
// produce N independent unwinds, never a cross-pair over-release. If the pair loop is ever
// parallelised, or any other lock is taken for this selection before the pre-search, this
// must switch to unlocking the specific token id instead. The
// "MultiplePartialPairLocksUnwindOncePerFailure" case in TestExactMatchPairSelectionUnit
// pins the invariant by asserting UnlockAll is called exactly once per failed second lock.
func (s *Selector) releasePreSearchLocks(ctx context.Context) {
	if err := s.locker.UnlockAll(ctx); err != nil {
		s.logger.DebugfContext(ctx, "exact-match pre-search: failed to release partial pair lock: %v", err)
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

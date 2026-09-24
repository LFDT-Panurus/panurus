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
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
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
	// byte-equal, so with realistic wallets every such bucket has size 1 and the
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
	// ones a hot-token incident hurts the most. 5x keeps the window meaningful while still
	// refusing to lock, say, a 200 EUR token when a 1 EUR one would do.
	//
	// The bound is deliberately anchor-relative and not remaining-relative: with the remaining
	// amount as the basis, any wallet whose smallest token already exceeds 5x the request would
	// see its very first lookahead candidate rejected, leaving
	// a window of size 1 and fully deterministic selection: the mechanism would be inert in
	// exactly the regime it exists for. Anchoring on the smallest sufficient token keeps the
	// selected token within 5x of what would have been locked regardless, in every regime.
	maxSufficiencyRatio = 5

	// maxBatchWindow caps how many candidates a single batch claim may cover. The window
	// otherwise grows until it covers the remaining amount, which is unbounded in the number
	// of tokens: a dust-heavy wallet paying a large amount can need tens of thousands of
	// inputs. The Postgres claim binds 2n+5 parameters (claimCandidates) and the wire protocol
	// refuses a statement with more than 65535, so an uncapped window would surface as a store
	// error - and the store-error branch refetches and rebuilds the same oversized window on
	// every attempt, so the selection burns its whole retry budget and fails where the
	// single-token path would have succeeded. The store refuses such a batch itself
	// (postgres.MaxLockBatchCandidates), but only this cap keeps a selection from ever
	// building one: 256 is two orders of magnitude inside that bound, and is chosen for the
	// statement size a claim should have rather than for the largest one it could have.
	//
	// The cap costs nothing in the common case and does not change what gets selected: the
	// outer loop simply claims another window when one is not enough, exactly as it already
	// does when the cache runs out mid-window.
	maxBatchWindow = 256
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
	mu        sync.Mutex // protects the cache and pending fields
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
	// Its length stays below sufficiencyWindow at all times, including in a wallet
	// where every token is individually sufficient and therefore every call grows a
	// full window: nextCandidate dequeues from pending before it touches the cache,
	// so a window is assembled out of pending first and only at most
	// sufficiencyWindow-1 of those entries are ever buffered back. The buffer is
	// bounded by the window size, not by the wallet or the cache size, and
	// refreshCandidates drops it outright whenever it installs a fresh cache.
	pending []*token2.UnspentTokenInWallet
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
		tokens, quantity, immediateRetries, err := m.selectWithoutMetrics(ctx, ownerFilter, q, tokenType, attempted)
		totalImmediateRetries += immediateRetries
		if err == nil || !errors.Is(err, token.SelectorSufficientButLockedFunds) {
			m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
			m.metrics.ImmediateRetries.Observe(float64(totalImmediateRetries))
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
			m.metrics.ImmediateRetries.Observe(float64(totalImmediateRetries))
			m.metrics.SelectionOutcome.With(outcomeLabel, "error").Add(1)

			return nil, nil, ctx.Err()
		}
		m.logger.DebugfContext(ctx, "Now it is our turn to retry...")
	}

	m.metrics.SelectionDuration.Observe(time.Since(start).Seconds())
	m.metrics.ImmediateRetries.Observe(float64(totalImmediateRetries))
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

	// Change-avoidance pre-search (k=1): before the greedy walk commits any tokens,
	// prefer a single unlocked candidate whose amount equals the full request, which
	// completes the selection with zero change. On any miss we fall through to the
	// greedy walk below, unchanged.
	greedyCacheFresh := false
	if s.exactMatch {
		s.metrics.ExactMatchAttempts.Add(1)
		ids, sum, ok, scanned := s.trySingleTokenExactMatch(ctx, owner, quantity, tokenType, attempted)
		if ok {
			s.metrics.ExactMatchHits.Add(1)
			s.logger.DebugfContext(ctx, "exact-match pre-search selected a single token of [%s:%s]", quantity.Decimal(), tokenType)

			return ids, sum, 0, nil
		}
		s.metrics.ExactMatchMisses.Add(1)
		greedyCacheFresh = scanned
	}

	// tokensLockedByOthersExist starts true only when the greedy cache is still the empty
	// placeholder, so the first exhaustion refreshes the cache before it can conclude
	// insufficient funds. When the pre-search already drained a fresh snapshot into the cache
	// (greedyCacheFresh), it starts false instead, so a clean exhaustion reports insufficient
	// funds immediately rather than re-fetching and re-locking the tokens this selector already
	// holds — the token-lock PRIMARY KEY(tx_id, idx) rejects every self-relock with
	// ErrTokenAlreadyLocked, which would otherwise set the flag back to true and spin until the
	// immediate-retry budget is exhausted.
	sum, selected, tokensLockedByOthersExist, immediateRetries := token2.NewZeroQuantity(s.precision), collections.NewSet[*token2.ID](), !greedyCacheFresh, 0
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
	// fetched records whether this call has already read the candidate source. Only a
	// *second* read is a retry of a scan that came up short, and only then is the source
	// told it is behind the store: see refreshCandidates.
	fetched := false
	for {
		remaining, remainingErr := quantity.Sub(sum)
		if remainingErr != nil {
			return nil, nil, immediateRetries, errors.Wrapf(remainingErr, "failed to compute remaining amount for [%s:%s]", owner.ID(), tokenType)
		}
		if t, err := s.nextCandidate(remaining, blacklisted.Contains); err != nil {
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
				//
				// A balance read behind the store - a lagging read replica, say - therefore
				// fast-fails a wallet whose freshly-added tokens are not visible yet. That is
				// not a cushion this check removed: before it, an empty scan with no observed
				// lock conflict returned SelectorInsufficientFunds from here unconditionally,
				// and SelectorInsufficientFunds exits StubbornSelector's backoff loop outright
				// (Select above), so lag failed the call then too. What the check adds is the
				// opposite: when the balance *does* cover the request, this no longer gives up
				// at all and falls through to a refetch. Closing the lag window itself means
				// making this read authoritative, which is a read-routing question - the
				// candidate scan (SpendableTokensIteratorBy) reads the same replica and so
				// cannot be more current than this is.
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

			var refreshErr error
			if immediateRetries, refreshErr = s.refreshCandidates(ctx, owner.ID(), tokenType, immediateRetries, fetched); refreshErr != nil {
				return nil, nil, immediateRetries, refreshErr
			}
			fetched = true
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
			// runs out, or maxBatchWindow is reached), then claim the whole window in one
			// call. This never claims more than the minimal covering prefix, so no
			// won-but-unselected token is ever left locked: every token claimed here either
			// ends up in selected below, or was never actually locked in the first place (a
			// lost race). A window cut short by either bound is claimed as it stands and the
			// outer loop comes back for the rest. The one case the store can leave outside
			// that invariant is a claim that commits and then fails while being read back,
			// which the error branch below handles by taking the verdicts that were read.
			window := []*token2.UnspentTokenInWallet{t}
			windowSum, err := token2.ToQuantity(t.Quantity, s.precision)
			if err != nil {
				return nil, nil, immediateRetries, errors.Wrapf(err, "invalid token [%s] found", t.Id)
			}
			for windowSum.Cmp(remaining) < 0 && len(window) < maxBatchWindow {
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
			outcome, lockErr := batchLocker.TryLockBatch(ctx, ids, owner.ID())
			if lockErr != nil {
				// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
				if errors.Is(lockErr, token.SelectorRateLimited) {
					return nil, nil, immediateRetries, lockErr
				}
				// A real store error (not per-token contention) failed the whole batch.
				// Don't blacklist: none of these tokens are known to be lost races. But they
				// were drained out of the cache to build the window, so simply continuing the
				// scan would leave them gone until the next refetch anyway — functionally
				// indistinguishable from blacklisting them, with the scan locking the larger
				// candidates behind them instead. Charge one unit of the immediate-retry budget
				// and refetch, which makes them visible again while keeping a permanently
				// failing store bounded by exactly the same budget lock contention is: one
				// attempt per retry, rather than re-walking the whole cache window by window on
				// every scan (or busy-looping on the same failing call, which is what requeuing
				// the window without charging the budget would do).
				s.logger.Warnf("Failed to batch-lock %d token(s): %v", len(window), lockErr)
				s.metrics.LockStoreErrors.Add(1)
				// A store may fail after the claim itself committed - the classifying read
				// of a Postgres INSERT ... RETURNING can break mid-result - in which case the
				// outcome it returns alongside the error holds the verdicts it did manage to
				// read, and those are locks this consumer tx now holds. Take them: a won lock
				// that does not reach selected is held for nothing until the transaction
				// settles.
				//
				// The candidates with no verdict at all stay unknown and are neither selected
				// nor blacklisted. Any of them this claim did in fact win is now hidden from
				// the refetch below by the notLocked anti-join, so this call cannot recover
				// it; it is released when the transaction settles and Unlock(txID) deletes
				// every row for this consumer tx, strays included, with the lease-expiry sweep
				// behind that. Bounding the loss to the unreadable rows is the point of
				// harvesting the readable ones.
				wonSet := collections.NewSet[token2.ID]()
				for _, id := range outcome.Won {
					wonSet.Add(*id)
				}
				for _, wt := range window {
					attempted.Add(wt.Id)
					if !wonSet.Contains(wt.Id) {
						continue
					}
					s.logger.DebugfContext(ctx, "Got the lock on token [%v] from a batch that then failed", wt)
					q, qErr := token2.ToQuantity(wt.Quantity, s.precision)
					if qErr != nil {
						return nil, nil, immediateRetries, errors.Wrapf(qErr, "invalid token [%s] found", wt.Id)
					}
					// Progress was made, so the immediate-retry budget is reset exactly as on
					// the success path. A store that keeps failing without winning anything
					// never reaches here and so stays bounded by the budget.
					immediateRetries = 0
					sum, qErr = sum.Add(q)
					if qErr != nil {
						return nil, nil, immediateRetries, errors.Wrapf(qErr, "failed to add quantity")
					}
					selected.Add(&wt.Id)
				}
				if sum.Cmp(quantity) >= 0 {
					return selected.ToSlice(), sum, immediateRetries, nil
				}
				sawNonBlacklistedCandidate = true
				tokensLockedByOthersExist = true
				var refreshErr error
				if immediateRetries, refreshErr = s.refreshCandidates(ctx, owner.ID(), tokenType, immediateRetries, fetched); refreshErr != nil {
					return nil, nil, immediateRetries, refreshErr
				}
				fetched = true

				continue
			}
			wonSet := collections.NewSet[token2.ID]()
			for _, id := range outcome.Won {
				wonSet.Add(*id)
			}
			// The store classifies every candidate it did not win: Stale holds the ones it
			// refused because they are no longer spendable, so the remainder of the window
			// lost a race (dbdriver.BatchLockOutcome). This is what keeps the batch path's
			// two failure modes as far apart as the single-token path below keeps
			// ErrTokenNotSpendable and ErrTokenAlreadyLocked - the counter they feed, the
			// retry semantics they imply, and whether the candidate cache is told it is
			// behind the store are all different per mode. A backend that leaves Stale empty
			// degrades to "every unwon candidate lost a race", which is what the batch path
			// used to do for all of them.
			staleSet := collections.NewSet[token2.ID]()
			for _, id := range outcome.Stale {
				staleSet.Add(*id)
			}
			// Collected over the window and acted on once below: a single refresh covers
			// every stale candidate the batch turned up, where one call per token would
			// re-invalidate an already-invalidated cache.
			sawStaleCandidate := false
			for _, wt := range window {
				attempted.Add(wt.Id)
				sawNonBlacklistedCandidate = true
				if !wonSet.Contains(wt.Id) {
					if staleSet.Contains(wt.Id) {
						// Stale, not contended: spendable when the snapshot this candidate came
						// from was taken, spent since. Same handling as the single-token path -
						// drop it for good, since no wait makes a spent token available again,
						// and leave tokensLockedByOthersExist alone, since nobody is holding
						// anything and the wallet's real balance stays the authority on whether
						// the request can be served at all.
						s.metrics.StaleCandidates.Add(1)
						s.logger.DebugfContext(ctx, "Dropping stale candidate [%s:%d]: no longer spendable", wt.Id.TxId, wt.Id.Index)
						blacklisted.Add(wt.Id)
						sawStaleCandidate = true

						continue
					}
					// Lost the race: another claimant holds this token's lock. Worth retrying,
					// so the flag is set and the caller keeps backing off rather than being
					// told the funds are not there.
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
			if sawStaleCandidate {
				// Blacklisting alone would leave the next scan re-reading the same stale
				// snapshot, so the candidate source is told to refresh: the one thing we now
				// know for certain is that it is behind the store. Done before the
				// sufficiency check rather than after, so a batch that both completes the
				// request and turns up a stale candidate still reports what it learned.
				s.invalidateCandidateCache(ctx, "Stale candidate observed in a batch claim")
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
				if errors.Is(lockErr, driver.ErrTokenNotSpendable) {
					// The candidate is stale, not contended: it was spendable when the
					// snapshot it came from was taken and has been spent since. Returning
					// it would hand the caller a token it cannot load, so drop it for good
					// - no wait makes a spent token available again - and do not set
					// tokensLockedByOthersExist: nobody is holding anything, so the
					// wallet's real balance (HasEnoughSpendableTokens) stays the authority
					// on whether this request can be served at all.
					//
					// Blacklisting alone would leave the next scan re-reading the same
					// stale snapshot, so the candidate source is told to refresh: the one
					// thing we now know for certain is that it is behind the store. See
					// #2395.
					s.metrics.StaleCandidates.Add(1)
					s.logger.DebugfContext(ctx, "Dropping stale candidate [%s:%d]: no longer spendable", t.Id.TxId, t.Id.Index)
					blacklisted.Add(t.Id)
					s.invalidateCandidateCache(ctx, "Stale candidate observed")

					continue
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
					s.metrics.LockStoreErrors.Add(1)
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
	if immediateRetries > maxImmediateRetries {
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

// trySingleTokenExactMatch runs the k=1 change-avoidance pre-search: it looks for a
// single candidate whose amount equals quantity and, if one is found and can be locked,
// returns it as a complete, change-free selection (ok=true). It reports ok=false — leaving
// the greedy walk to run — when no exact candidate exists, every exact candidate is
// currently locked by another process, or any error occurs. It is strictly best-effort: it
// never fails the selection and never holds a lock unless it returns that lock as the
// winning result.
//
// scanned reports whether the whole wallet iterator was drained and its snapshot seeded into
// s.cache (including the empty-wallet case, where there is nothing to seed). It is true only
// at the two exits reached after a full read, and false at every early bail (fetch error,
// iterator error, rate-limit denial, or a Close landing mid-scan), where the cache is left
// untouched for the greedy walk to fetch for itself. The caller uses it to decide whether the
// greedy walk's first exhaustion may conclude insufficient funds (fresh snapshot) or must
// first refresh the cache (early bail left it empty).
//
// Every token the pre-search tries to lock is recorded in attempted, the same per-Select()
// set the greedy walk fills, so a change-free hit is still counted by
// observeDistinctTokensAttempted and every lost lock race increments LockConflicts exactly
// as in the greedy walk. A failure that is not a lock conflict (a real store error that
// TryLock collapses into the same !locked result) is logged at warn, not hidden at debug.
//
// The snapshot is read from a fresh iterator in a single linear pass that collects the
// exact-amount matches directly, with no sort — O(n) in the wallet size. Unlike the greedy
// walk, which short-circuits once the running sum covers the request, this pass always drains
// to the end and parses every token's amount, so on a miss it adds a full extra scan ahead of
// the greedy walk that still runs. The design doc's maxScanCandidates cap is not implemented
// here: the pre-search is left deliberately unbounded so it still finds the exact token in the
// large, dust-heavy wallets it most helps, at the cost of that scan. When several candidates
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
func (s *Selector) trySingleTokenExactMatch(ctx context.Context, owner token.OwnerFilter, quantity token2.Quantity, tokenType token2.Type, attempted collections.Set[token2.ID]) ([]*token2.ID, token2.Quantity, bool, bool) {
	it, err := s.fetcher.UnspentTokensIteratorBy(ctx, owner.ID(), tokenType)
	if err != nil {
		s.logger.DebugfContext(ctx, "exact-match pre-search: failed to fetch candidates: %v", err)

		return nil, nil, false, false
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

			return nil, nil, false, false
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

		return nil, nil, false, true
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
			return nil, nil, false, false
		}
		id := matches[i]
		// Count the attempt before the outcome is known, so the per-Select() distinct-token
		// fan-out counts the pre-search like the greedy walk.
		attempted.Add(id)
		locked, lockErr := s.locker.TryLock(ctx, &id, owner.ID())
		if locked {
			// amount == quantity for every match, so the request is covered exactly, no change.
			return []*token2.ID{&id}, quantity, true, false
		}
		if errors.Is(lockErr, token.SelectorRateLimited) {
			// A rate-limit denial is a hard stop: the greedy walk will hit the same limit and
			// surface it consistently. Do not seed; the request is aborting anyway.
			return nil, nil, false, false
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

	return nil, nil, false, true
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

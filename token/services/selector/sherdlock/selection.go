/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
)

// selection is the state of one selectInternal call: what it has locked so far, what it
// has learned about the candidates it was offered, and how much of its immediate-retry
// budget it has spent. Each step of the selection loop is a method on it.
type selection struct {
	s         *Selector
	owner     token.OwnerFilter
	tokenType token2.Type
	quantity  token2.Quantity
	// attempted is owned by the caller; see selectInternal.
	attempted collections.Set[token2.ID]

	sum      token2.Quantity
	selected collections.Set[*token2.ID]
	// tokensLockedByOthersExist records whether the current scan saw a token someone else
	// holds, which is what makes an exhausted scan worth retrying rather than a plain
	// insufficient-funds verdict.
	tokensLockedByOthersExist bool
	immediateRetries          int
	// blacklisted holds tokens this call has already lost a lock race on, so a
	// refetch does not immediately re-attempt (and re-lose) the same race
	// against the same hot token: see #2395, where one token was re-proposed
	// in a loop for over six minutes. It is scoped to this single Select
	// call, not process-global, so a token that is genuinely freed by
	// another process is reconsidered on the caller's next Select call.
	blacklisted collections.Set[token2.ID]
	// sawNonBlacklistedCandidate tracks whether the current scan of the
	// cache (since the last refetch) produced at least one candidate that
	// was not already blacklisted. If a whole scan sees nothing but
	// blacklisted tokens, the blacklist is excluding every candidate we
	// have, so it is cleared: otherwise a genuinely-contended wallet
	// with no other tokens would turn a lost race into a permanent false
	// insufficient-funds instead of ever retrying.
	sawNonBlacklistedCandidate bool
	// batchLocker is non-nil when the underlying store can claim several candidates in one
	// round trip (see BatchLocker). When present, the loop claims a covering window
	// of candidates per attempt instead of one token at a time, which is what actually
	// dissolves the ordering hot spot from #2395: reading it once up front means the
	// decision is made per Select call, not per iteration.
	batchLocker BatchTokenLocker
	// fetched records whether this call has already read the candidate source. Only a
	// *second* read is a retry of a scan that came up short, and only then is the source
	// told it is behind the store: see refreshCandidates.
	fetched bool
}

func (s *Selector) newSelection(owner token.OwnerFilter, tokenType token2.Type, quantity token2.Quantity, attempted collections.Set[token2.ID]) *selection {
	sel := &selection{
		s:                         s,
		owner:                     owner,
		tokenType:                 tokenType,
		quantity:                  quantity,
		attempted:                 attempted,
		sum:                       token2.NewZeroQuantity(s.precision),
		selected:                  collections.NewSet[*token2.ID](),
		tokensLockedByOthersExist: true,
		blacklisted:               collections.NewSet[token2.ID](),
	}
	if batchLocker, ok := s.locker.(BatchTokenLocker); ok {
		sel.batchLocker = batchLocker
	}

	return sel
}

// step considers the next candidate, and reports whether the requested quantity is now
// covered.
func (sel *selection) step(ctx context.Context) (bool, error) {
	remaining, err := sel.quantity.Sub(sel.sum)
	if err != nil {
		return false, errors.Wrapf(err, "failed to compute remaining amount for [%s:%s]", sel.owner.ID(), sel.tokenType)
	}
	t, err := sel.s.nextCandidate(remaining, sel.blacklisted.Contains)
	if err != nil {
		return false, errors.Wrapf(err, "failed to get tokens for [%s:%s]", sel.owner.ID(), sel.tokenType)
	}

	switch {
	case t == nil:
		return false, sel.onScanExhausted(ctx)
	case sel.blacklisted.Contains(t.Id):
		// Already lost the race on this token earlier in this same
		// Select call: don't re-attempt it, just note that a locked
		// token exists so the caller keeps retrying/backing off instead
		// of reporting insufficient funds.
		sel.s.logger.DebugfContext(ctx, "Skipping blacklisted token [%v]: already lost a lock race on it this call", t.Id)
		sel.tokensLockedByOthersExist = true

		return false, nil
	case sel.batchLocker != nil:
		return sel.lockWindow(ctx, t, remaining)
	default:
		return sel.lockOne(ctx, t)
	}
}

// onScanExhausted handles a scan of the candidate cache that ran out before the request
// was covered: it fails fast when the wallet cannot cover the request at all, and
// otherwise refetches the candidates.
func (sel *selection) onScanExhausted(ctx context.Context) error {
	if !sel.tokensLockedByOthersExist {
		if err := sel.checkWalletCanCover(ctx); err != nil {
			return err
		}
	}

	if !sel.sawNonBlacklistedCandidate && !sel.blacklisted.Empty() {
		sel.s.logger.DebugfContext(ctx, "Blacklist excluded every candidate this scan; clearing it so freed tokens can be retried.")
		sel.blacklisted = collections.NewSet[token2.ID]()
	}
	sel.sawNonBlacklistedCandidate = false

	if err := sel.refresh(ctx); err != nil {
		return err
	}
	sel.tokensLockedByOthersExist = false

	return nil
}

// checkWalletCanCover reports token.SelectorInsufficientFunds when the wallet's whole
// spendable balance cannot cover the request.
//
// The candidate query excludes already-locked tokens (#2395, mechanism 3), so an empty
// scan that never saw a lock conflict is ambiguous: it may mean this wallet truly has no
// more funds, or that every remaining token is currently locked by someone else and was
// hidden from us entirely. Disambiguate with a direct, lock-ignoring existence check before
// giving up. HasEnoughSpendableTokens sums the wallet's whole spendable balance: a wallet
// that cannot cover the request can never satisfy this Select call no matter how the rest
// gets unlocked, so fail immediately instead of spending the immediate-retry/backoff budget
// on a request that can never succeed.
//
// The comparison is against the full requested quantity, not the remaining amount: the
// query deliberately ignores locks, so the total it reports still includes the tokens this
// very call has already locked and counted into sum. Comparing against remaining
// (= quantity - sum) would put those tokens on both sides, degenerating into
// `total >= total - sum` — true as soon as anything at all was selected, which is precisely
// when the fast fail is needed. Since the total already includes sum, `total >= quantity`
// is the equivalent, non-double-counting form of `total - sum >= remaining`.
//
// A balance read behind the store - a lagging read replica, say - therefore fast-fails a
// wallet whose freshly-added tokens are not visible yet. That is not a cushion this check
// removed: before it, an empty scan with no observed lock conflict returned
// SelectorInsufficientFunds unconditionally, and SelectorInsufficientFunds exits
// StubbornSelector's backoff loop outright (Select above), so lag failed the call then too.
// What the check adds is the opposite: when the balance *does* cover the request, this no
// longer gives up at all and falls through to a refetch. Closing the lag window itself
// means making this read authoritative, which is a read-routing question - the candidate
// scan (SpendableTokensIteratorBy) reads the same replica and so cannot be more current
// than this is.
func (sel *selection) checkWalletCanCover(ctx context.Context) error {
	hasEnough, err := sel.s.fetcher.HasEnoughSpendableTokens(ctx, sel.owner.ID(), sel.tokenType, sel.quantity.ToBigInt())
	if err != nil {
		return errors.Wrapf(err, "failed to check for locked tokens for [%s:%s]", sel.owner.ID(), sel.tokenType)
	}
	if !hasEnough {
		return errors.Wrapf(
			token.SelectorInsufficientFunds,
			"insufficient funds, only [%s] tokens of type [%s] are available, but [%s] were requested and no other process has any tokens locked",
			sel.sum.Decimal(),
			sel.tokenType,
			sel.quantity.Decimal(),
		)
	}

	return nil
}

// refresh charges the immediate-retry budget and refetches the candidates; see
// refreshCandidates.
func (sel *selection) refresh(ctx context.Context) error {
	var err error
	if sel.immediateRetries, err = sel.s.refreshCandidates(ctx, sel.owner.ID(), sel.tokenType, sel.immediateRetries, sel.fetched); err != nil {
		return err
	}
	sel.fetched = true

	return nil
}

// add records a token this call holds the lock on, and reports whether the request is
// now covered.
func (sel *selection) add(ctx context.Context, t *token2.UnspentTokenInWallet) (bool, error) {
	q, err := token2.ToQuantity(t.Quantity, sel.s.precision)
	if err != nil {
		return false, errors.Wrapf(err, "invalid token [%s] found", t.Id)
	}
	sel.s.logger.DebugfContext(ctx, "Found token [%s] to add: [%s:%s].", t.Id, q.Decimal(), t.Type)
	// Progress was made, so the immediate-retry budget is reset. A store that keeps failing
	// without winning anything never reaches here and so stays bounded by the budget.
	sel.immediateRetries = 0
	if sel.sum, err = sel.sum.Add(q); err != nil {
		return false, errors.Wrapf(err, "failed to add quantity")
	}
	sel.selected.Add(&t.Id)

	return sel.covered(), nil
}

// covered reports whether the tokens selected so far cover the request.
func (sel *selection) covered() bool {
	return sel.sum.Cmp(sel.quantity) >= 0
}

// lockOne tries to lock a single candidate.
func (sel *selection) lockOne(ctx context.Context, t *token2.UnspentTokenInWallet) (bool, error) {
	// Counted once here, before the outcome is known, so a later third
	// outcome branch cannot forget to record the attempt.
	sel.attempted.Add(t.Id)
	sel.sawNonBlacklistedCandidate = true
	if locked, lockErr := sel.s.locker.TryLock(ctx, &t.Id, sel.owner.ID()); !locked {
		return false, sel.onLockFailed(ctx, t, lockErr)
	}
	sel.s.logger.DebugfContext(ctx, "Got the lock on token [%v]", t)

	return sel.add(ctx, t)
}

// onLockFailed handles a single candidate this call did not get the lock on. Only a
// rate-limit denial is returned as an error; every other failure lets the scan go on.
func (sel *selection) onLockFailed(ctx context.Context, t *token2.UnspentTokenInWallet, lockErr error) error {
	switch {
	case errors.Is(lockErr, token.SelectorRateLimited):
		// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
		return lockErr
	case errors.Is(lockErr, driver.ErrTokenNotSpendable):
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
		sel.dropStale(ctx, t)
		sel.s.invalidateCandidateCache(ctx, "Stale candidate observed")

		return nil
	case errors.Is(lockErr, driver.ErrTokenAlreadyLocked):
		// Lost the race: someone else holds this token. This is the
		// expected, common case under contention, not a DB error.
		sel.s.metrics.LockConflicts.Add(1)
		sel.s.logger.DebugfContext(ctx, "Lost lock race on token [%s:%d]: already locked by another process", t.Id.TxId, t.Id.Index)
		sel.blacklisted.Add(t.Id)
	default:
		// A real store error (not a lock conflict) collapsed into the
		// same !locked branch by TryLock. Only the log line separates
		// the two: to the caller this still reads as ordinary
		// contention, so a store outage surfaces as locked funds
		// rather than as an error. See #2395.
		sel.s.metrics.LockStoreErrors.Add(1)
		sel.s.logger.WarnfContext(ctx, "Failed to lock token [%s:%d]: %v", t.Id.TxId, t.Id.Index, lockErr)
	}
	sel.tokensLockedByOthersExist = true

	return nil
}

// dropStale blacklists a candidate that is no longer spendable.
func (sel *selection) dropStale(ctx context.Context, t *token2.UnspentTokenInWallet) {
	sel.s.metrics.StaleCandidates.Add(1)
	sel.s.logger.DebugfContext(ctx, "Dropping stale candidate [%s:%d]: no longer spendable", t.Id.TxId, t.Id.Index)
	sel.blacklisted.Add(t.Id)
}

// lockWindow grows a window of candidates from t and claims it in one call.
//
// The window grows from t until it covers the remaining amount (or the cache runs out, or
// Tuning.MaxBatchWindow is reached). This never claims more than the minimal covering
// prefix, so no won-but-unselected token is ever left locked: every token claimed here
// either ends up in selected, or was never actually locked in the first place (a lost
// race). A window cut short by either bound is claimed as it stands and the outer loop
// comes back for the rest. The one case the store can leave outside that invariant is a
// claim that commits and then fails while being read back, which onBatchFailed handles by
// taking the verdicts that were read.
func (sel *selection) lockWindow(ctx context.Context, t *token2.UnspentTokenInWallet, remaining token2.Quantity) (bool, error) {
	window, err := sel.growWindow(t, remaining)
	if err != nil {
		return false, err
	}

	ids := make([]*token2.ID, len(window))
	for i := range window {
		ids[i] = &window[i].Id
	}
	outcome, lockErr := sel.batchLocker.TryLockBatch(ctx, ids, sel.owner.ID())
	if lockErr != nil {
		return sel.onBatchFailed(ctx, window, outcome, lockErr)
	}

	sawStaleCandidate, err := sel.applyBatchOutcome(ctx, window, outcome)
	if err != nil {
		return false, err
	}
	if sawStaleCandidate {
		// Blacklisting alone would leave the next scan re-reading the same stale
		// snapshot, so the candidate source is told to refresh: the one thing we now
		// know for certain is that it is behind the store. Done before the
		// sufficiency check rather than after, so a batch that both completes the
		// request and turns up a stale candidate still reports what it learned.
		sel.s.invalidateCandidateCache(ctx, "Stale candidate observed in a batch claim")
	}

	return sel.covered(), nil
}

// growWindow returns t followed by as many further non-blacklisted candidates as it takes
// to cover remaining, up to Tuning.MaxBatchWindow.
func (sel *selection) growWindow(t *token2.UnspentTokenInWallet, remaining token2.Quantity) ([]*token2.UnspentTokenInWallet, error) {
	window := []*token2.UnspentTokenInWallet{t}
	windowSum, err := token2.ToQuantity(t.Quantity, sel.s.precision)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid token [%s] found", t.Id)
	}
	for windowSum.Cmp(remaining) < 0 && len(window) < sel.s.tuning.MaxBatchWindow {
		next, err := sel.s.dequeue()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get tokens for [%s:%s]", sel.owner.ID(), sel.tokenType)
		}
		if next == nil {
			break
		}
		if sel.blacklisted.Contains(next.Id) {
			continue
		}
		nq, err := token2.ToQuantity(next.Quantity, sel.s.precision)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid token [%s] found", next.Id)
		}
		if windowSum, err = windowSum.Add(nq); err != nil {
			return nil, errors.Wrapf(err, "failed to add quantity")
		}
		window = append(window, next)
	}

	return window, nil
}

// onBatchFailed handles a batch claim that failed as a whole.
//
// A rate-limit denial from the locker is a hard stop: abort instead of retrying.
//
// Anything else is a real store error (not per-token contention) that failed the whole
// batch. Don't blacklist: none of these tokens are known to be lost races. But they were
// drained out of the cache to build the window, so simply continuing the scan would leave
// them gone until the next refetch anyway — functionally indistinguishable from
// blacklisting them, with the scan locking the larger candidates behind them instead.
// Charge one unit of the immediate-retry budget and refetch, which makes them visible again
// while keeping a permanently failing store bounded by exactly the same budget lock
// contention is: one attempt per retry, rather than re-walking the whole cache window by
// window on every scan (or busy-looping on the same failing call, which is what requeuing
// the window without charging the budget would do).
//
// A store may fail after the claim itself committed - the classifying read of a Postgres
// INSERT ... RETURNING can break mid-result - in which case the outcome it returns
// alongside the error holds the verdicts it did manage to read, and those are locks this
// consumer tx now holds. Take them: a won lock that does not reach selected is held for
// nothing until the transaction settles.
//
// The candidates with no verdict at all stay unknown and are neither selected nor
// blacklisted. Any of them this claim did in fact win is now hidden from the refetch by the
// notLocked anti-join, so this call cannot recover it; it is released when the transaction
// settles and Unlock(txID) deletes every row for this consumer tx, strays included, with
// the lease-expiry sweep behind that. Bounding the loss to the unreadable rows is the point
// of harvesting the readable ones.
func (sel *selection) onBatchFailed(ctx context.Context, window []*token2.UnspentTokenInWallet, outcome driver.BatchLockOutcome, lockErr error) (bool, error) {
	if errors.Is(lockErr, token.SelectorRateLimited) {
		return false, lockErr
	}
	sel.s.logger.Warnf("Failed to batch-lock %d token(s): %v", len(window), lockErr)
	sel.s.metrics.LockStoreErrors.Add(1)

	won := idSet(outcome.Won)
	for _, wt := range window {
		sel.attempted.Add(wt.Id)
		if !won.Contains(wt.Id) {
			continue
		}
		sel.s.logger.DebugfContext(ctx, "Got the lock on token [%v] from a batch that then failed", wt)
		if _, err := sel.add(ctx, wt); err != nil {
			return false, err
		}
	}
	if sel.covered() {
		return true, nil
	}
	sel.sawNonBlacklistedCandidate = true
	sel.tokensLockedByOthersExist = true

	return false, sel.refresh(ctx)
}

// applyBatchOutcome records the verdict of a successful batch claim for every candidate in
// window, and reports whether any of them turned out to be stale.
//
// The store classifies every candidate it did not win: Stale holds the ones it refused
// because they are no longer spendable, so the remainder of the window lost a race
// (dbdriver.BatchLockOutcome). This is what keeps the batch path's two failure modes as far
// apart as the single-token path keeps ErrTokenNotSpendable and ErrTokenAlreadyLocked - the
// counter they feed, the retry semantics they imply, and whether the candidate cache is
// told it is behind the store are all different per mode. A backend that leaves Stale empty
// degrades to "every unwon candidate lost a race", which is what the batch path used to do
// for all of them.
//
// Stale candidates are collected over the window and acted on once by the caller: a single
// refresh covers every stale candidate the batch turned up, where one call per token would
// re-invalidate an already-invalidated cache.
func (sel *selection) applyBatchOutcome(ctx context.Context, window []*token2.UnspentTokenInWallet, outcome driver.BatchLockOutcome) (bool, error) {
	won, stale := idSet(outcome.Won), idSet(outcome.Stale)
	sawStaleCandidate := false
	for _, wt := range window {
		sel.attempted.Add(wt.Id)
		sel.sawNonBlacklistedCandidate = true
		switch {
		case won.Contains(wt.Id):
			sel.s.logger.DebugfContext(ctx, "Got the lock on token [%v]", wt)
			if _, err := sel.add(ctx, wt); err != nil {
				return false, err
			}
		case stale.Contains(wt.Id):
			// Stale, not contended: spendable when the snapshot this candidate came
			// from was taken, spent since. Same handling as the single-token path -
			// drop it for good, since no wait makes a spent token available again,
			// and leave tokensLockedByOthersExist alone, since nobody is holding
			// anything and the wallet's real balance stays the authority on whether
			// the request can be served at all.
			sel.dropStale(ctx, wt)
			sawStaleCandidate = true
		default:
			// Lost the race: another claimant holds this token's lock. Worth retrying,
			// so the flag is set and the caller keeps backing off rather than being
			// told the funds are not there.
			sel.s.metrics.LockConflicts.Add(1)
			sel.s.logger.Infof("Lost lock race on token [%s:%d]: already locked by another process", wt.Id.TxId, wt.Id.Index)
			sel.blacklisted.Add(wt.Id)
			sel.tokensLockedByOthersExist = true
		}
	}

	return sawStaleCandidate, nil
}

// idSet collects ids into a set of values.
func idSet(ids []*token2.ID) collections.Set[token2.ID] {
	set := collections.NewSet[token2.ID]()
	for _, id := range ids {
		set.Add(*id)
	}

	return set
}

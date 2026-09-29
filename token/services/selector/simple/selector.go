/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

type QueryService interface {
	UnspentTokensIterator(ctx context.Context) (*token.UnspentTokensIterator, error)
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type, limit int) (driver.UnspentTokensIterator, error)
	GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error)
}

type Locker interface {
	// Lock locks the token id for the consumer transaction txID on behalf of the given
	// owner (the wallet the tokens are selected for, ownerFilter.ID()).
	// owner lets a Locker implementation apply per-wallet policies such as rate limiting.
	// To deny a lock for policy reasons, return an error wrapping token.SelectorRateLimited:
	// the selector then aborts immediately instead of retrying.
	Lock(ctx context.Context, owner string, id *token2.ID, txID string, reclaim bool) (string, error)
	// UnlockIDs unlocks the passed IDs for the given owner. It returns the list of tokens
	// that were not locked in the first place among those passed.
	UnlockIDs(ctx context.Context, owner string, ids ...*token2.ID) []*token2.ID
	UnlockByTxID(ctx context.Context, txID string)
	IsLocked(id *token2.ID) bool
}

type selector struct {
	txID         string
	locker       Locker
	queryService QueryService
	precision    uint64

	maxRetries           int
	timeout              time.Duration
	requestCertification bool

	// Resource limits to prevent algorithmic attacks
	maxTokensPerSelection int
	maxLockAttempts       int
	selectionTimeout      time.Duration
}

// selectionCounters holds the per-selection tallies used for limit enforcement
// and error messages. It lives on the stack of a single Select call rather than
// on *selector, so a selector shared between goroutines (for example one handed
// in via transferOpts.Selector) keeps no shared mutable state and stays
// race-free by construction.
type selectionCounters struct {
	// tokensIterated and lockAttempts are cumulative across retry cycles and
	// are reported in error messages only.
	tokensIterated int
	lockAttempts   int
}

// Select selects tokens to be spent based on ownership, quantity, and type
func (s *selector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	if ownerFilter == nil || len(ownerFilter.ID()) == 0 {
		return nil, nil, errors.Errorf("no owner filter specified")
	}

	// Per-selection counters live on the stack (see selectionCounters) so a
	// shared *selector stays race-free.
	counters := &selectionCounters{}

	// Create timeout context if configured
	timeoutCtx, cancel := withSelectionTimeout(ctx, s.selectionTimeout)
	defer cancel()

	// Use timeout context for selection
	result, quantity, err := s.selectByID(timeoutCtx, ownerFilter, q, tokenType, counters)

	// Check if we hit the timeout
	if errors.Is(err, context.DeadlineExceeded) {
		// Use original context for cleanup to ensure it completes
		s.locker.UnlockByTxID(ctx, s.txID)

		// Wrap the sentinel so callers can tell a timeout apart from a
		// genuine failure, as sherdlock's selector already does.
		return nil, nil, errors.WithMessagef(
			token.SelectorTimedOut,
			"token selection aborted: exceeded timeout (%v) after examining %d tokens and %d lock attempts",
			s.selectionTimeout, counters.tokensIterated, counters.lockAttempts,
		)
	}

	return result, quantity, err
}

func (s *selector) Close() error { return nil }

func (s *selector) concurrencyCheck(ctx context.Context, ids []*token2.ID) error {
	_, err := s.queryService.GetTokens(ctx, ids...)

	return err
}

func (s *selector) selectByID(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type, counters *selectionCounters) ([]*token2.ID, token2.Quantity, error) {
	var toBeSpent []*token2.ID
	var sum token2.Quantity
	var potentialSumWithLocked token2.Quantity
	target, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to convert quantity")
	}
	id := ownerFilter.ID()

	// Fetch one row more than the iteration cap. That extra row is the probe
	// that tells a page which is full because the wallet holds more tokens than
	// we are allowed to examine (limit exceeded) apart from a page which is full
	// because the wallet holds exactly maxTokensPerSelection tokens (a complete,
	// if large, wallet). A non-positive cap means "unbounded": pass 0 straight
	// through so the query applies no limit.
	queryLimit := s.maxTokensPerSelection
	if queryLimit > 0 {
		queryLimit++
	}

	actualRetries := 0
	var unspentTokens driver.UnspentTokensIterator
	defer func() {
		if unspentTokens != nil {
			unspentTokens.Close()
		}
	}()
	for {
		// Check retry cycle limit. A non-positive maxRetries means "unbounded":
		// the selection then runs until it succeeds, funds prove insufficient, or
		// the wall-clock selection timeout fires.
		actualRetries++
		if s.maxRetries > 0 && actualRetries > s.maxRetries {
			s.locker.UnlockByTxID(ctx, s.txID)

			return nil, nil, errors.Errorf(
				"token selection aborted: exceeded max retries (%d) after examining %d tokens and %d lock attempts",
				s.maxRetries, counters.tokensIterated, counters.lockAttempts,
			)
		}

		if unspentTokens != nil {
			unspentTokens.Close()
		}
		logger.DebugfContext(ctx, "start token selection, iteration [%d/%d] (tokens examined: %d, lock attempts: %d)",
			actualRetries, s.maxRetries, counters.tokensIterated, counters.lockAttempts)
		unspentTokens, err = s.queryService.UnspentTokensIteratorBy(ctx, id, tokenType, queryLimit)
		if err != nil {
			return nil, nil, errors.Wrap(err, "token selection failed")
		}
		logger.DebugfContext(ctx, "select token for a quantity of [%s] of type [%s]", q, tokenType)

		// The query above is capped per cycle, so both the iteration and the
		// lock-attempt budgets are per cycle, not for the whole selection.
		tokensIteratedThisCycle := 0
		lockAttemptsThisCycle := 0

		// First select only certified
		sum = token2.NewZeroQuantity(s.precision)
		potentialSumWithLocked = token2.NewZeroQuantity(s.precision)
		toBeSpent = nil
		var toBeCertified []*token2.ID

		reclaim := s.maxRetries == 1 || actualRetries > 1
		for {
			t, err := unspentTokens.Next()
			if err != nil {
				return nil, nil, errors.Wrap(err, "token selection failed")
			}
			if t == nil {
				break
			}

			// Check token iteration limit (only count actual tokens, not nil).
			// A non-positive maxTokensPerSelection means "unbounded". We fetched
			// maxTokensPerSelection+1 rows, so seeing the (cap+1)th token here is
			// what proves the wallet holds more tokens than we may examine.
			counters.tokensIterated++
			tokensIteratedThisCycle++
			if s.maxTokensPerSelection > 0 && tokensIteratedThisCycle > s.maxTokensPerSelection {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.WithMessagef(
					token.SelectorResourceLimitExceeded,
					"token selection aborted: exceeded max token iteration limit (%d tokens)",
					s.maxTokensPerSelection,
				)
			}

			q, err := token2.ToQuantity(t.Quantity, s.precision)
			if err != nil {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.Wrap(err, "failed to convert quantity")
			}

			// Check lock attempt limit. A non-positive maxLockAttempts means
			// "unbounded". The counter is per cycle so it stays comparable to
			// maxTokensPerSelection (which Validate keeps it above): a cumulative
			// counter would multiply by the retry count and trip this ceiling long
			// before the typed SelectorSufficientButLockedFunds could be returned.
			counters.lockAttempts++
			lockAttemptsThisCycle++
			if s.maxLockAttempts > 0 && lockAttemptsThisCycle > s.maxLockAttempts {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.WithMessagef(
					token.SelectorResourceLimitExceeded,
					"token selection aborted: exceeded max lock attempts (%d) after examining %d tokens",
					s.maxLockAttempts, counters.tokensIterated,
				)
			}

			// lock the token on behalf of the selecting wallet
			if _, lockErr := s.locker.Lock(ctx, id, &t.Id, s.txID, reclaim); lockErr != nil {
				// A rate-limit denial from the Locker is a hard stop: abort instead of retrying.
				if errors.Is(lockErr, token.SelectorRateLimited) {
					s.locker.UnlockIDs(ctx, id, toBeSpent...)
					s.locker.UnlockIDs(ctx, id, toBeCertified...)

					return nil, nil, lockErr
				}

				var addErr error
				potentialSumWithLocked, addErr = potentialSumWithLocked.Add(q)
				if addErr != nil {
					s.locker.UnlockIDs(ctx, id, toBeSpent...)
					s.locker.UnlockIDs(ctx, id, toBeCertified...)

					return nil, nil, errors.Wrap(addErr, "failed to add locked quantity")
				}

				logger.DebugfContext(ctx, "token [%s,%v] cannot be locked [%s]", q, tokenType, lockErr)

				continue
			}

			// Append token
			logger.DebugfContext(ctx, "adding quantity [%s]", q.Decimal())
			toBeSpent = append(toBeSpent, &t.Id)
			sum, err = sum.Add(q)
			if err != nil {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.Wrap(err, "failed to add quantity")
			}
			potentialSumWithLocked, err = potentialSumWithLocked.Add(q)
			if err != nil {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.Wrap(err, "failed to add quantity")
			}

			if target.Cmp(sum) <= 0 {
				break
			}
		}

		concurrencyIssue := false
		if target.Cmp(sum) <= 0 {
			err := s.concurrencyCheck(ctx, toBeSpent)
			if err == nil {
				return toBeSpent, sum, nil
			}
			concurrencyIssue = true
			logger.Errorf("concurrency issue, some of the tokens might not exist anymore [%s]", err)
		}

		// Unlock and check the conditions for a retry
		s.locker.UnlockIDs(ctx, id, toBeSpent...)
		s.locker.UnlockIDs(ctx, id, toBeCertified...)

		if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
			// funds are potentially enough but they are locked
			logger.DebugfContext(ctx, "token selection: sufficient funds but partially locked")
		} else if target.Cmp(potentialSumWithLocked) > 0 {
			// Insufficient funds with no locked tokens. Because the query fetches
			// maxTokensPerSelection+1 rows, a wallet holding more tokens than this
			// selection may examine trips the iteration limit inside the loop above
			// and returns SelectorResourceLimitExceeded before ever reaching here.
			// Arriving here therefore means the iterator surfaced every token the
			// wallet has — including the boundary case of a wallet holding exactly
			// maxTokensPerSelection tokens — so the funds really are insufficient.
			// Fail immediately instead of retrying until the timeout.
			logger.DebugfContext(ctx, "token selection: insufficient funds, no tokens locked, failing immediately")

			return nil, nil, errors.WithMessagef(
				token.SelectorInsufficientFunds,
				"insufficient funds, only [%s] tokens of type [%s] are available, but [%s] were requested and no other process has any tokens locked",
				sum.Decimal(),
				tokenType,
				target.Decimal(),
			)
		}

		// The retry cycle limit is reached here, on the last allowed iteration, so
		// that the caller still gets the typed reason the selection failed. The
		// guard at the top of the loop only catches a misconfigured maxRetries.
		// A non-positive maxRetries means "unbounded", so this typed-failure block
		// is skipped and the selection keeps retrying until the timeout fires.
		if s.maxRetries > 0 && actualRetries >= s.maxRetries {
			// it is time to fail but how?
			if concurrencyIssue {
				logger.DebugfContext(ctx, "concurrency issue, some of the tokens might not exist anymore")

				return nil, nil, errors.WithMessagef(
					token.SelectorSufficientFundsButConcurrencyIssue,
					"token selection aborted: exceeded max retries (%d) after examining %d tokens and %d lock attempts: sufficient funds but concurrency issue, potential [%s] tokens of type [%s] were available",
					s.maxRetries, counters.tokensIterated, counters.lockAttempts, potentialSumWithLocked, tokenType,
				)
			}

			if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
				// funds are potentially enough but they are locked
				logger.DebugfContext(ctx, "token selection: it is time to fail but how, sufficient funds but locked")

				return nil, nil, errors.WithMessagef(
					token.SelectorSufficientButLockedFunds,
					"token selection aborted: exceeded max retries (%d) after examining %d tokens and %d lock attempts: sufficient but partially locked funds, potential [%s] tokens of type [%s] are available",
					s.maxRetries, counters.tokensIterated, counters.lockAttempts, potentialSumWithLocked.Decimal(), tokenType,
				)
			}

			// funds are insufficient
			logger.DebugfContext(ctx, "token selection: it is time to fail but how, insufficient funds")

			return nil, nil, errors.WithMessagef(
				token.SelectorInsufficientFunds,
				"insufficient funds, only [%s] tokens of type [%s] are available, but [%s] were requested and no other process has any tokens locked",
				sum.Decimal(),
				tokenType,
				target.Decimal(),
			)
		}

		backoff := s.retryBackoff()
		logger.DebugfContext(ctx, "token selection: let's wait [%v] before retry...", backoff)
		time.Sleep(backoff)
	}
}

// withSelectionTimeout bounds ctx by the configured selection timeout.
//
// A non-positive timeout means "no timeout": passing it straight to
// context.WithTimeout yields an already-expired context, so every Select would
// abort after examining zero tokens.
func withSelectionTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, timeout)
}

// retryBackoff returns a random duration in [0, timeout), so transactions
// that lost a race for the same funds don't all retry at the same instant
// (same jittering pattern as sherdlock's selector).
func (s *selector) retryBackoff() time.Duration {
	if s.timeout <= 0 {
		return 0
	}

	return time.Duration(rand.Int64N(int64(s.timeout)))
}

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

	// Resource tracking counters (reset per selection)
	tokensIteratedCount int
	lockAttemptsCount   int

	// tokensIteratedThisCycle counts the tokens examined in the current retry
	// cycle only. The DB already caps each cycle at maxTokensPerSelection, so
	// the per-cycle count is what the limit and the "we have seen everything"
	// check must compare against; tokensIteratedCount stays cumulative and is
	// only reported in error messages.
	tokensIteratedThisCycle int
}

// Select selects tokens to be spent based on ownership, quantity, and type
func (s *selector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	if ownerFilter == nil || len(ownerFilter.ID()) == 0 {
		return nil, nil, errors.Errorf("no owner filter specified")
	}

	// Reset resource tracking counters for this selection
	s.tokensIteratedCount = 0
	s.lockAttemptsCount = 0
	s.tokensIteratedThisCycle = 0

	// Create timeout context if configured
	timeoutCtx, cancel := withSelectionTimeout(ctx, s.selectionTimeout)
	defer cancel()

	// Use timeout context for selection
	result, quantity, err := s.selectByID(timeoutCtx, ownerFilter, q, tokenType)

	// Check if we hit the timeout
	if errors.Is(err, context.DeadlineExceeded) {
		// Use original context for cleanup to ensure it completes
		s.locker.UnlockByTxID(ctx, s.txID)

		// Wrap the sentinel so callers can tell a timeout apart from a
		// genuine failure, as sherdlock's selector already does.
		return nil, nil, errors.WithMessagef(
			token.SelectorTimedOut,
			"token selection aborted: exceeded timeout (%v) after examining %d tokens and %d lock attempts",
			s.selectionTimeout, s.tokensIteratedCount, s.lockAttemptsCount,
		)
	}

	return result, quantity, err
}

func (s *selector) Close() error { return nil }

func (s *selector) concurrencyCheck(ctx context.Context, ids []*token2.ID) error {
	_, err := s.queryService.GetTokens(ctx, ids...)

	return err
}

func (s *selector) selectByID(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	var toBeSpent []*token2.ID
	var sum token2.Quantity
	var potentialSumWithLocked token2.Quantity
	target, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to convert quantity")
	}
	id := ownerFilter.ID()

	actualRetries := 0
	var unspentTokens driver.UnspentTokensIterator
	defer func() {
		if unspentTokens != nil {
			unspentTokens.Close()
		}
	}()
	for {
		// Check retry cycle limit
		actualRetries++
		if actualRetries > s.maxRetries {
			s.locker.UnlockByTxID(ctx, s.txID)

			return nil, nil, errors.Errorf(
				"token selection aborted: exceeded max retries (%d) after examining %d tokens and %d lock attempts",
				s.maxRetries, s.tokensIteratedCount, s.lockAttemptsCount,
			)
		}

		if unspentTokens != nil {
			unspentTokens.Close()
		}
		logger.DebugfContext(ctx, "start token selection, iteration [%d/%d] (tokens examined: %d, lock attempts: %d)",
			actualRetries, s.maxRetries, s.tokensIteratedCount, s.lockAttemptsCount)
		unspentTokens, err = s.queryService.UnspentTokensIteratorBy(ctx, id, tokenType, s.maxTokensPerSelection)
		if err != nil {
			return nil, nil, errors.Wrap(err, "token selection failed")
		}
		logger.DebugfContext(ctx, "select token for a quantity of [%s] of type [%s]", q, tokenType)

		// The query above is capped at maxTokensPerSelection rows, so the
		// iteration budget is per cycle, not for the whole selection.
		s.tokensIteratedThisCycle = 0

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

			// Check token iteration limit (only count actual tokens, not nil)
			s.tokensIteratedCount++
			s.tokensIteratedThisCycle++
			if s.tokensIteratedThisCycle > s.maxTokensPerSelection {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.Errorf(
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

			// Check lock attempt limit
			s.lockAttemptsCount++
			if s.lockAttemptsCount > s.maxLockAttempts {
				s.locker.UnlockIDs(ctx, id, toBeSpent...)
				s.locker.UnlockIDs(ctx, id, toBeCertified...)

				return nil, nil, errors.Errorf(
					"token selection aborted: exceeded max lock attempts (%d) after examining %d tokens",
					s.maxLockAttempts, s.tokensIteratedCount,
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
			// Insufficient funds with no locked tokens. Whether that is the
			// final answer depends on how much of the wallet this cycle got to
			// see, which is decided per cycle: the query is capped at
			// maxTokensPerSelection rows, and the cumulative counter grows past
			// that cap across cycles even when every cycle saw the full set.
			if s.tokensIteratedThisCycle >= s.maxTokensPerSelection {
				// A full page came back, so the wallet holds more tokens than
				// this selection is allowed to examine. The query is ordered
				// deterministically, so a retry re-reads the same page: report
				// the limit as the reason rather than spinning on it.
				logger.DebugfContext(ctx, "token selection: iteration limit reached with a full page of tokens")

				return nil, nil, errors.Errorf(
					"token selection aborted: exceeded max token iteration limit (%d tokens)",
					s.maxTokensPerSelection,
				)
			}

			// A short page means the iterator surfaced every token the wallet
			// has, so the funds really are insufficient. Fail immediately
			// instead of retrying until the timeout.
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
		if actualRetries >= s.maxRetries {
			// it is time to fail but how?
			if concurrencyIssue {
				logger.DebugfContext(ctx, "concurrency issue, some of the tokens might not exist anymore")

				return nil, nil, errors.WithMessagef(
					token.SelectorSufficientFundsButConcurrencyIssue,
					"token selection aborted: exceeded max retries (%d) after examining %d tokens and %d lock attempts: sufficient funds but concurrency issue, potential [%s] tokens of type [%s] were available",
					s.maxRetries, s.tokensIteratedCount, s.lockAttemptsCount, potentialSumWithLocked, tokenType,
				)
			}

			if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
				// funds are potentially enough but they are locked
				logger.DebugfContext(ctx, "token selection: it is time to fail but how, sufficient funds but locked")

				return nil, nil, errors.WithMessagef(
					token.SelectorSufficientButLockedFunds,
					"token selection aborted: exceeded max retries (%d) after examining %d tokens and %d lock attempts: sufficient but partially locked funds, potential [%s] tokens of type [%s] are available",
					s.maxRetries, s.tokensIteratedCount, s.lockAttemptsCount, potentialSumWithLocked.Decimal(), tokenType,
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

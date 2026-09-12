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
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type) (driver.UnspentTokensIterator, error)
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

	numRetry             int
	timeout              time.Duration
	requestCertification bool
}

// Select selects tokens to be spent based on ownership, quantity, and type
func (s *selector) Select(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	if ownerFilter == nil || len(ownerFilter.ID()) == 0 {
		return nil, nil, errors.Errorf("no owner filter specified")
	}

	return s.selectByID(ctx, ownerFilter, q, tokenType)
}

func (s *selector) Close() error { return nil }

func (s *selector) concurrencyCheck(ctx context.Context, ids []*token2.ID) error {
	_, err := s.queryService.GetTokens(ctx, ids...)

	return err
}

// lockAndAccumulateToken tries to lock candidate token t (worth q); on success it appends t to
// toBeSpent and adds q to both sum and potentialSumWithLocked, on failure it only adds q to
// potentialSumWithLocked (for diagnostics). On any internal error, it unlocks everything
// accumulated so far (including t, if it was just locked) before returning. done reports whether
// target has now been reached.
func (s *selector) lockAndAccumulateToken(
	ctx context.Context,
	owner string,
	t *token2.UnspentToken,
	q, sum, potentialSumWithLocked, target token2.Quantity,
	tokenType token2.Type,
	reclaim bool,
	toBeSpent, toBeCertified []*token2.ID,
) (newToBeSpent []*token2.ID, newSum, newPotentialSumWithLocked token2.Quantity, done bool, err error) {
	if _, lockErr := s.locker.Lock(ctx, owner, &t.Id, s.txID, reclaim); lockErr != nil {
		// A rate-limit denial from the Locker is a hard stop: abort instead of retrying.
		if errors.Is(lockErr, token.SelectorRateLimited) {
			s.locker.UnlockIDs(ctx, owner, toBeSpent...)
			s.locker.UnlockIDs(ctx, owner, toBeCertified...)

			return nil, nil, nil, false, lockErr
		}

		newPotentialSumWithLocked, err = potentialSumWithLocked.Add(q)
		if err != nil {
			s.locker.UnlockIDs(ctx, owner, toBeSpent...)
			s.locker.UnlockIDs(ctx, owner, toBeCertified...)

			return nil, nil, nil, false, errors.Wrap(err, "failed to add locked quantity")
		}

		logger.DebugfContext(ctx, "token [%s,%v] cannot be locked [%s]", q, tokenType, lockErr)

		return toBeSpent, sum, newPotentialSumWithLocked, false, nil
	}

	// Append token
	logger.DebugfContext(ctx, "adding quantity [%s]", q.Decimal())
	newToBeSpent = append(toBeSpent, &t.Id)
	newSum, err = sum.Add(q)
	if err != nil {
		s.locker.UnlockIDs(ctx, owner, newToBeSpent...)
		s.locker.UnlockIDs(ctx, owner, toBeCertified...)

		return nil, nil, nil, false, errors.Wrap(err, "failed to add quantity")
	}
	newPotentialSumWithLocked, err = potentialSumWithLocked.Add(q)
	if err != nil {
		s.locker.UnlockIDs(ctx, owner, newToBeSpent...)
		s.locker.UnlockIDs(ctx, owner, toBeCertified...)

		return nil, nil, nil, false, errors.Wrap(err, "failed to add quantity")
	}

	return newToBeSpent, newSum, newPotentialSumWithLocked, target.Cmp(newSum) <= 0, nil
}

// collectTokensForTarget pulls tokens from unspentTokens, locking and accumulating the ones that
// aren't already locked by someone else, until target is reached or the iterator is exhausted.
// It also tracks the potential sum including tokens found locked by others (for diagnostics on
// failure), and unlocks everything it locked so far before returning an error.
func (s *selector) collectTokensForTarget(
	ctx context.Context,
	owner string,
	unspentTokens driver.UnspentTokensIterator,
	target token2.Quantity,
	tokenType token2.Type,
	reclaim bool,
) (toBeSpent, toBeCertified []*token2.ID, sum, potentialSumWithLocked token2.Quantity, err error) {
	sum = token2.NewZeroQuantity(s.precision)
	potentialSumWithLocked = token2.NewZeroQuantity(s.precision)
	numNext := 0

	for {
		t, err := unspentTokens.Next()
		numNext++
		if err != nil {
			return nil, nil, nil, nil, errors.Wrap(err, "token selection failed")
		}
		if t == nil {
			return toBeSpent, toBeCertified, sum, potentialSumWithLocked, nil
		}

		q, err := token2.ToQuantity(t.Quantity, s.precision)
		if err != nil {
			s.locker.UnlockIDs(ctx, owner, toBeSpent...)
			s.locker.UnlockIDs(ctx, owner, toBeCertified...)

			return nil, nil, nil, nil, errors.Wrap(err, "failed to convert quantity")
		}

		var done bool
		toBeSpent, sum, potentialSumWithLocked, done, err = s.lockAndAccumulateToken(ctx, owner, t, q, sum, potentialSumWithLocked, target, tokenType, reclaim, toBeSpent, toBeCertified)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if done {
			return toBeSpent, toBeCertified, sum, potentialSumWithLocked, nil
		}
	}
}

// buildSelectionFailureError builds the final error to return once all retries are exhausted,
// distinguishing a concurrency issue, sufficient-but-locked funds, and plain insufficient funds.
func buildSelectionFailureError(ctx context.Context, concurrencyIssue bool, target, potentialSumWithLocked, sum token2.Quantity, tokenType token2.Type) error {
	// it is time to fail but how?
	if concurrencyIssue {
		logger.DebugfContext(ctx, "concurrency issue, some of the tokens might not exist anymore")

		return errors.WithMessagef(
			token.SelectorSufficientFundsButConcurrencyIssue,
			"token selection failed: sufficient funds but concurrency issue, potential [%s] tokens of type [%s] were available", potentialSumWithLocked, tokenType,
		)
	}

	if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
		// funds are potentially enough but they are locked
		logger.DebugfContext(ctx, "token selection: it is time to fail but how, sufficient funds but locked")

		return errors.WithMessagef(
			token.SelectorSufficientButLockedFunds,
			"token selection failed: sufficient but partially locked funds, potential [%s] tokens of type [%s] are available", potentialSumWithLocked.Decimal(), tokenType,
		)
	}

	// funds are insufficient
	logger.DebugfContext(ctx, "token selection: it is time to fail but how, insufficient funds")

	return errors.WithMessagef(
		token.SelectorInsufficientFunds,
		"token selection failed: insufficient funds, only [%s] tokens of type [%s] are available", sum.Decimal(), tokenType,
	)
}

// selectAttemptResult is the outcome of one full selection attempt (one pass over the unspent
// tokens iterator).
type selectAttemptResult struct {
	spent                  []*token2.ID
	sum                    token2.Quantity
	success                bool
	concurrencyIssue       bool
	potentialSumWithLocked token2.Quantity
	err                    error
}

// attemptSelection runs one full selection attempt: collect candidate tokens, check whether the
// target was reached and, if so, whether a concurrency check confirms they're still valid. On
// anything short of success, it unlocks everything it collected before returning.
func (s *selector) attemptSelection(ctx context.Context, owner string, unspentTokens driver.UnspentTokensIterator, target token2.Quantity, tokenType token2.Type, reclaim bool) selectAttemptResult {
	toBeSpent, toBeCertified, sum, potentialSumWithLocked, err := s.collectTokensForTarget(ctx, owner, unspentTokens, target, tokenType, reclaim)
	if err != nil {
		return selectAttemptResult{err: err}
	}

	concurrencyIssue := false
	if target.Cmp(sum) <= 0 {
		if ccErr := s.concurrencyCheck(ctx, toBeSpent); ccErr == nil {
			return selectAttemptResult{spent: toBeSpent, sum: sum, success: true}
		} else {
			concurrencyIssue = true
			logger.Errorf("concurrency issue, some of the tokens might not exist anymore [%s]", ccErr)
		}
	}

	// Unlock and check the conditions for a retry
	s.locker.UnlockIDs(ctx, owner, toBeSpent...)
	s.locker.UnlockIDs(ctx, owner, toBeCertified...)

	if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
		// funds are potentially enough but they are locked
		logger.DebugfContext(ctx, "token selection: sufficient funds but partially locked")
	}

	return selectAttemptResult{sum: sum, concurrencyIssue: concurrencyIssue, potentialSumWithLocked: potentialSumWithLocked}
}

func (s *selector) selectByID(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	target, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to convert quantity")
	}
	id := ownerFilter.ID()

	i := 0
	var unspentTokens driver.UnspentTokensIterator
	defer func() {
		if unspentTokens != nil {
			unspentTokens.Close()
		}
	}()
	for {
		if unspentTokens != nil {
			unspentTokens.Close()
		}
		logger.DebugfContext(ctx, "start token selection, iteration [%d/%d]", i, s.numRetry)
		unspentTokens, err = s.queryService.UnspentTokensIteratorBy(ctx, id, tokenType)
		if err != nil {
			return nil, nil, errors.Wrap(err, "token selection failed")
		}
		logger.DebugfContext(ctx, "select token for a quantity of [%s] of type [%s]", q, tokenType)

		reclaim := s.numRetry == 1 || i > 0
		result := s.attemptSelection(ctx, id, unspentTokens, target, tokenType, reclaim)
		if result.err != nil {
			return nil, nil, result.err
		}
		if result.success {
			return result.spent, result.sum, nil
		}

		i++
		if i >= s.numRetry {
			return nil, nil, buildSelectionFailureError(ctx, result.concurrencyIssue, target, result.potentialSumWithLocked, result.sum, tokenType)
		}

		backoff := s.retryBackoff()
		logger.DebugfContext(ctx, "token selection: let's wait [%v] before retry...", backoff)
		time.Sleep(backoff)
	}
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

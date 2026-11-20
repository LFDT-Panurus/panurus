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
	metrics              *Metrics
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

// selectByID walks the wallet's unspent tokens, locking as it goes, and retries the whole scan
// from scratch whenever a lock race or a concurrency check fails.
//
// Each iteration holds at most one database connection at a time: the unspentTokens cursor is
// closed before the nested concurrencyCheck query runs (see below). Overlapping the two used to
// make every in-flight Select pin two connections, which self-deadlocks any pool smaller than
// the number of concurrent selectors - observed against #2395's hot-token workload
// (TestHotTokenContentionSimpleDriver) with MaxOpenConns=10 and 30 concurrent selects.
func (s *selector) selectByID(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	target, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to convert quantity")
	}
	id := ownerFilter.ID()

	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, errors.Wrap(err, "token selection aborted")
		}
		logger.DebugfContext(ctx, "start token selection, iteration [%d/%d]", i, s.numRetry)
		res, err := s.scan(ctx, id, q, tokenType, target, s.numRetry == 1 || i > 0)
		if err != nil {
			return nil, nil, err
		}

		done, concurrencyIssue := s.settle(ctx, res, target)
		if done {
			return res.toBeSpent, res.sum, nil
		}

		if err := s.endAttempt(ctx, id, res, target, tokenType, concurrencyIssue, i+1 >= s.numRetry); err != nil {
			return nil, nil, err
		}

		if err := s.waitBeforeRetry(ctx); err != nil {
			return nil, nil, err
		}
	}
}

// settle reports whether res covers target with tokens that all still exist. concurrencyIssue
// is true when res covered target but some of its tokens were gone by the time they were
// checked.
func (s *selector) settle(ctx context.Context, res *scanResult, target token2.Quantity) (done bool, concurrencyIssue bool) {
	if target.Cmp(res.sum) > 0 {
		return false, false
	}
	if err := s.concurrencyCheck(ctx, res.toBeSpent); err != nil {
		logger.Errorf("concurrency issue, some of the tokens might not exist anymore [%s]", err)

		return false, true
	}

	return true, false
}

// endAttempt releases what an attempt that did not settle locked, and returns the error to stop
// with: the context's if the caller gave up meanwhile, the final verdict if this was the last
// attempt, and nil if the selection should back off and retry.
func (s *selector) endAttempt(ctx context.Context, id string, res *scanResult, target token2.Quantity, tokenType token2.Type, concurrencyIssue bool, last bool) error {
	// Unlock and check the conditions for a retry
	s.locker.UnlockIDs(ctx, id, res.toBeSpent...)

	// A caller that gave up during this attempt gets ctx.Err(), not a sentinel describing
	// funds it no longer cares about. Checking here, before the backoff, also keeps a
	// zero-length jitter timer from winning the select in waitBeforeRetry against ctx.Done().
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, "token selection aborted")
	}

	if res.sufficientButLocked(target) {
		// funds are potentially enough but they are locked
		logger.DebugfContext(ctx, "token selection: sufficient funds but partially locked")
	}

	if last {
		return selectionFailure(ctx, res, target, tokenType, concurrencyIssue)
	}

	return nil
}

// scanResult is what one scan of the wallet's unspent tokens locked and saw.
type scanResult struct {
	// toBeSpent are the tokens the scan locked.
	toBeSpent []*token2.ID
	// sum is the total of toBeSpent.
	sum token2.Quantity
	// potentialSumWithLocked also counts the tokens the scan could not lock.
	potentialSumWithLocked token2.Quantity
}

// sufficientButLocked reports whether the scan saw enough funds to cover target, but some
// of them were locked by someone else.
func (r *scanResult) sufficientButLocked(target token2.Quantity) bool {
	return target.Cmp(r.potentialSumWithLocked) <= 0 && r.potentialSumWithLocked.Cmp(r.sum) != 0
}

// scan walks the wallet's unspent tokens once, locking them until they cover target.
//
// The cursor is closed when scan returns, before the caller's concurrencyCheck issues its
// own query. The retry path opens a fresh one, so closing it here costs nothing and keeps
// this selector down to a single connection checkout at any instant, which is what stops a
// bounded pool from deadlocking under concurrent selects (#2395).
func (s *selector) scan(ctx context.Context, id string, q string, tokenType token2.Type, target token2.Quantity, reclaim bool) (*scanResult, error) {
	unspentTokens, err := s.queryService.UnspentTokensIteratorBy(ctx, id, tokenType)
	if err != nil {
		return nil, errors.Wrap(err, "token selection failed")
	}
	defer unspentTokens.Close()
	logger.DebugfContext(ctx, "select token for a quantity of [%s] of type [%s]", q, tokenType)

	res := &scanResult{
		sum:                    token2.NewZeroQuantity(s.precision),
		potentialSumWithLocked: token2.NewZeroQuantity(s.precision),
	}
	for {
		t, err := unspentTokens.Next()
		if err != nil {
			return nil, errors.Wrap(err, "token selection failed")
		}
		if t == nil {
			return res, nil
		}
		if err := s.lockAndAdd(ctx, id, t, tokenType, reclaim, res); err != nil {
			s.locker.UnlockIDs(ctx, id, res.toBeSpent...)

			return nil, err
		}
		if target.Cmp(res.sum) <= 0 {
			return res, nil
		}
	}
}

// lockAndAdd tries to lock t on behalf of the selecting wallet. A token it locks is added
// to res.toBeSpent; one it cannot lock only counts towards res.potentialSumWithLocked.
// Only a rate-limit denial or a malformed quantity is returned as an error.
func (s *selector) lockAndAdd(ctx context.Context, id string, t *token2.UnspentToken, tokenType token2.Type, reclaim bool, res *scanResult) error {
	q, err := token2.ToQuantity(t.Quantity, s.precision)
	if err != nil {
		return errors.Wrap(err, "failed to convert quantity")
	}

	if _, lockErr := s.locker.Lock(ctx, id, &t.Id, s.txID, reclaim); lockErr != nil {
		// A rate-limit denial from the Locker is a hard stop: abort instead of retrying.
		if errors.Is(lockErr, token.SelectorRateLimited) {
			return lockErr
		}
		if res.potentialSumWithLocked, err = res.potentialSumWithLocked.Add(q); err != nil {
			return errors.Wrap(err, "failed to add locked quantity")
		}
		logger.DebugfContext(ctx, "token [%s,%v] cannot be locked [%s]", q, tokenType, lockErr)

		return nil
	}

	logger.DebugfContext(ctx, "adding quantity [%s]", q.Decimal())
	res.toBeSpent = append(res.toBeSpent, &t.Id)
	if res.sum, err = res.sum.Add(q); err != nil {
		return errors.Wrap(err, "failed to add quantity")
	}
	if res.potentialSumWithLocked, err = res.potentialSumWithLocked.Add(q); err != nil {
		return errors.Wrap(err, "failed to add quantity")
	}

	return nil
}

// selectionFailure is the error selectByID gives up with once it has run out of retries.
func selectionFailure(ctx context.Context, res *scanResult, target token2.Quantity, tokenType token2.Type, concurrencyIssue bool) error {
	if concurrencyIssue {
		logger.DebugfContext(ctx, "concurrency issue, some of the tokens might not exist anymore")

		return errors.WithMessagef(
			token.SelectorSufficientFundsButConcurrencyIssue,
			"token selection failed: sufficient funds but concurrency issue, potential [%s] tokens of type [%s] were available", res.potentialSumWithLocked, tokenType,
		)
	}

	if res.sufficientButLocked(target) {
		// funds are potentially enough but they are locked
		logger.DebugfContext(ctx, "token selection: it is time to fail but how, sufficient funds but locked")

		return errors.WithMessagef(
			token.SelectorSufficientButLockedFunds,
			"token selection failed: sufficient but partially locked funds, potential [%s] tokens of type [%s] are available", res.potentialSumWithLocked.Decimal(), tokenType,
		)
	}

	// funds are insufficient
	logger.DebugfContext(ctx, "token selection: it is time to fail but how, insufficient funds")

	return errors.WithMessagef(
		token.SelectorInsufficientFunds,
		"token selection failed: insufficient funds, only [%s] tokens of type [%s] are available", res.sum.Decimal(), tokenType,
	)
}

// waitBeforeRetry backs off for retryBackoff before the next scan, and returns an error if
// the context is done first.
func (s *selector) waitBeforeRetry(ctx context.Context) error {
	backoff := s.retryBackoff()
	logger.DebugfContext(ctx, "token selection: let's wait [%v] before retry...", backoff)
	if s.timeout <= 0 {
		return nil
	}
	backoffStart := time.Now()
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		s.observeBackoff(backoffStart)

		return nil
	case <-ctx.Done():
		s.observeBackoff(backoffStart)

		return errors.Wrap(ctx.Err(), "token selection aborted while backing off")
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

// observeBackoff records the time elapsed since start as a backoff wait, if metrics are set.
func (s *selector) observeBackoff(start time.Time) {
	if s.metrics != nil {
		s.metrics.BackoffDuration.Observe(time.Since(start).Seconds())
	}
}

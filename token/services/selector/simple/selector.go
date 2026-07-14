/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
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
	Lock(ctx context.Context, id *token2.ID, txID string, reclaim bool) (string, error)
	// UnlockIDs unlocks the passed IDS. It returns the list of tokens that were not locked in the first place among
	// those passed.
	UnlockIDs(ctx context.Context, ids ...*token2.ID) []*token2.ID
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

func (s *selector) selectByID(ctx context.Context, ownerFilter token.OwnerFilter, q string, tokenType token2.Type) ([]*token2.ID, token2.Quantity, error) {
	var toBeSpent []*token2.ID
	var sum token2.Quantity
	var potentialSumWithLocked token2.Quantity
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

		// First select only certified
		sum = token2.NewZeroQuantity(s.precision)
		potentialSumWithLocked = token2.NewZeroQuantity(s.precision)
		toBeSpent = nil
		var toBeCertified []*token2.ID

		reclaim := s.numRetry == 1 || i > 0
		numNext := 0
		for {
			t, err := unspentTokens.Next()
			numNext++
			if err != nil {
				return nil, nil, errors.Wrap(err, "token selection failed")
			}
			if t == nil {
				break
			}

			q, err := token2.ToQuantity(t.Quantity, s.precision)
			if err != nil {
				s.locker.UnlockIDs(ctx, toBeSpent...)
				s.locker.UnlockIDs(ctx, toBeCertified...)

				return nil, nil, errors.Wrap(err, "failed to convert quantity")
			}

			// lock the token
			if _, lockErr := s.locker.Lock(ctx, &t.Id, s.txID, reclaim); lockErr != nil {
				var addErr error
				potentialSumWithLocked, addErr = potentialSumWithLocked.Add(q)
				if addErr != nil {
					s.locker.UnlockIDs(ctx, toBeSpent...)
					s.locker.UnlockIDs(ctx, toBeCertified...)

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
				s.locker.UnlockIDs(ctx, toBeSpent...)
				s.locker.UnlockIDs(ctx, toBeCertified...)

				return nil, nil, errors.Wrap(err, "failed to add quantity")
			}
			potentialSumWithLocked, err = potentialSumWithLocked.Add(q)
			if err != nil {
				s.locker.UnlockIDs(ctx, toBeSpent...)
				s.locker.UnlockIDs(ctx, toBeCertified...)

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
		s.locker.UnlockIDs(ctx, toBeSpent...)
		s.locker.UnlockIDs(ctx, toBeCertified...)

		if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
			// funds are potentially enough but they are locked
			logger.DebugfContext(ctx, "token selection: sufficient funds but partially locked")
		}

		i++
		if i >= s.numRetry {
			// it is time to fail but how?
			if concurrencyIssue {
				logger.DebugfContext(ctx, "concurrency issue, some of the tokens might not exist anymore")

				return nil, nil, errors.WithMessagef(
					token.SelectorSufficientFundsButConcurrencyIssue,
					"token selection failed: sufficient funds but concurrency issue, potential [%s] tokens of type [%s] were available", potentialSumWithLocked, tokenType,
				)
			}

			if target.Cmp(potentialSumWithLocked) <= 0 && potentialSumWithLocked.Cmp(sum) != 0 {
				// funds are potentially enough but they are locked
				logger.DebugfContext(ctx, "token selection: it is time to fail but how, sufficient funds but locked")

				return nil, nil, errors.WithMessagef(
					token.SelectorSufficientButLockedFunds,
					"token selection failed: sufficient but partially locked funds, potential [%s] tokens of type [%s] are available", potentialSumWithLocked.Decimal(), tokenType,
				)
			}

			// funds are insufficient
			logger.DebugfContext(ctx, "token selection: it is time to fail but how, insufficient funds")

			return nil, nil, errors.WithMessagef(
				token.SelectorInsufficientFunds,
				"token selection failed: insufficient funds, only [%s] tokens of type [%s] are available", sum.Decimal(), tokenType,
			)
		}

		logger.DebugfContext(ctx, "token selection: let's wait [%v] before retry...", s.timeout)
		time.Sleep(s.timeout)
	}
}

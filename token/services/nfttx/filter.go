/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
)

// Filter is a filter for NFTCC
type Filter interface {
	// ContainsToken returns true if the passed token is recognized, false otherwise.
	ContainsToken(token *token2.UnspentToken) bool
}

type QueryService interface {
	UnspentTokensIterator(ctx context.Context) (*token.UnspentTokensIterator, error)
	UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type) (driver.UnspentTokensIterator, error)
	GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error)
}

type filter struct {
	wallet       string
	queryService QueryService
	precision    uint64
}

func NewFilter(wallet string, service QueryService, precision uint64) *filter {
	return &filter{
		wallet:       wallet,
		queryService: service,
		precision:    precision,
	}
}

func (s *filter) Filter(ctx context.Context, filter Filter, q string) ([]*token2.ID, error) {
	if filter == nil {
		return nil, errors.New("filter is nil")
	}
	ids, _, err := s.selectByFilter(ctx, filter, q)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to select tokens")
	}

	return ids, nil
}

func (s *filter) selectByFilter(ctx context.Context, filter Filter, q string) ([]*token2.ID, token2.Quantity, error) {
	var toBeSpent []*token2.ID
	var sum token2.Quantity
	target, err := token2.ToQuantity(q, s.precision)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to convert quantity")
	}

	unspentTokens, err := s.queryService.UnspentTokensIteratorBy(ctx, s.wallet, "")
	if err != nil {
		return nil, nil, errors.Wrap(err, "token selection failed")
	}
	unspentTokens = iterators.Filter(unspentTokens, filter.ContainsToken)

	defer unspentTokens.Close()
	logger.Debugf("select token for a quantity of [%s]", q)

	sum = token2.NewZeroQuantity(s.precision)
	toBeSpent = nil
	for {
		t, err := unspentTokens.Next()
		if err != nil {
			return nil, nil, errors.Wrap(err, "token selection failed")
		}
		if t == nil {
			break
		}

		q, err := token2.ToQuantity(t.Quantity, s.precision)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to convert quantity")
		}

		// Append token
		logger.Debugf("adding quantity [%s]", q.Decimal())
		toBeSpent = append(toBeSpent, &t.Id)
		sum, err = sum.Add(q)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to add quantity")
		}
		if target.Cmp(sum) <= 0 {
			return toBeSpent, sum, nil
		}
	}

	return nil, nil, ErrNoResults
}

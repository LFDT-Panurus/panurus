/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx

import (
	"context"
	"encoding/base64"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/marshaller"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/tidwall/gjson"
)

var (
	// ErrNoResults is returned when no results are found
	ErrNoResults = errors.New("no results found")
)

type vault interface {
	GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error)
}

type selector interface {
	Filter(ctx context.Context, filter Filter, q string) ([]*token2.ID, error)
}

type QueryExecutor struct {
	selector
	vault
	precision uint64
}

func NewQueryExecutor(sp token.ServiceProvider, wallet string, precision uint64, opts ...token.ServiceOption) (*QueryExecutor, error) {
	tms, err := token.GetManagementService(sp, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get token management service")
	}
	qe := tms.Vault().NewQueryEngine()

	return &QueryExecutor{
		selector: NewFilter(
			wallet,
			qe,
			tms.PublicParametersManager().PublicParameters().Precision(),
		),
		vault:     qe,
		precision: precision,
	}, nil
}

func (s *QueryExecutor) QueryByKey(ctx context.Context, state any, key string, value string) error {
	ids, err := s.Filter(ctx, &jsonFilter{
		key:   key,
		value: value,
	}, "1")
	if err != nil {
		if errors.Is(errors.Cause(err), ErrNoResults) {
			return ErrNoResults
		}

		return errors.Wrap(err, "failed to filter")
	}
	tokens, err := s.GetTokens(ctx, ids...)
	if err != nil {
		return errors.Wrap(err, "failed to get tokens")
	}
	for _, t := range tokens {
		q, err := token2.ToQuantity(t.Quantity, s.precision)
		if err != nil {
			return errors.Wrap(err, "failed to convert quantity")
		}
		if q.Cmp(token2.NewOneQuantity(s.precision)) == 0 {
			// this is the token
			decoded, err := base64.StdEncoding.DecodeString(string(t.Type))
			if err != nil {
				return errors.Wrap(err, "failed to decode type")
			}
			if err := marshaller.Unmarshal(decoded, state); err == nil {
				return errors.Wrap(err, "failed to unmarshal state")
			}
		}
	}

	return ErrNoResults
}

type jsonFilter struct {
	key, value string
}

func (j *jsonFilter) ContainsToken(token *token2.UnspentToken) bool {
	decoded, err := base64.StdEncoding.DecodeString(string(token.Type))
	if err != nil {
		logger.Debugf("failed to decode token type [%s]", token.Type)

		return false
	}
	logger.Debugf("decoded token type [%s]", string(decoded))
	res := gjson.Get(string(decoded), j.key)
	if res.Type == gjson.String {
		return res.String() == j.value
	}
	logger.Debugf("res [%s] for [%s,%s]", res, j.key, j.value)

	return false
}

// NewTestQueryExecutor returns a new QueryExecutor for testing purposes
func NewTestQueryExecutor(s selector, v vault, p uint64) *QueryExecutor {
	return &QueryExecutor{
		selector:  s,
		vault:     v,
		precision: p,
	}
}

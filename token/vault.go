/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token provides token vault functionality for querying and storing token state and transaction status.
package token

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// TxStatus is the status of a transaction
type TxStatus = driver.TxStatus

const (
	// Unknown is the status of a transaction that is unknown
	Unknown = driver.Unknown
	// Pending is the status of a transaction that has been submitted to the ledger
	Pending = driver.Pending
	// Confirmed is the status of a transaction that has been confirmed by the ledger
	Confirmed = driver.Confirmed
	// Deleted is the status of a transaction that has been deleted due to a failure to commit
	Deleted = driver.Deleted
	// Orphan is the status of a transaction that never reached the ledger
	Orphan = driver.Orphan
)

// QueryEngine models a token query engine
type QueryEngine struct {
	qe     driver.QueryEngine
	logger logging.Logger

	// Variables used to control retry condition
	NumRetries int
	RetryDelay time.Duration
}

func NewQueryEngine(logger logging.Logger, qe driver.QueryEngine, numRetries int, retryDelay time.Duration) *QueryEngine {
	return &QueryEngine{logger: logger, qe: qe, NumRetries: numRetries, RetryDelay: retryDelay}
}

// IsMine returns true is the given token is in this vault and therefore owned by this client
func (q *QueryEngine) IsMine(ctx context.Context, id *token.ID) (bool, error) {
	return q.qe.IsMine(ctx, id)
}

// UnspentTokensIterator returns an iterator over all unspent tokens stored in the vault
func (q *QueryEngine) UnspentTokensIterator(ctx context.Context) (*UnspentTokensIterator, error) {
	it, err := q.qe.UnspentTokensIterator(ctx)
	if err != nil {
		return nil, err
	}

	return &UnspentTokensIterator{UnspentTokensIterator: it}, nil
}

// UnspentTokensIteratorBy is an iterator over all unspent tokens in this vault owned by passed wallet id and whose token type matches the passed token type
func (q *QueryEngine) UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token.Type) (driver.UnspentTokensIterator, error) {
	it, err := q.qe.UnspentTokensIteratorBy(ctx, id, tokenType)
	if err != nil {
		return nil, err
	}

	return &UnspentTokensIterator{UnspentTokensIterator: it}, nil
}

// ListUnspentTokens returns a list of all unspent tokens stored in the vault
func (q *QueryEngine) ListUnspentTokens(ctx context.Context) (*token.UnspentTokens, error) {
	return q.qe.ListUnspentTokens(ctx)
}

func (q *QueryEngine) ListAuditTokens(ctx context.Context, ids ...*token.ID) ([]*token.Token, error) {
	var tokens []*token.Token
	var err error

	for i := range q.NumRetries {
		tokens, err = q.qe.ListAuditTokens(ctx, ids...)

		if err != nil {
			// check if there is any token id whose corresponding transaction is pending
			// if there is, then wait a bit and retry to load the outputs
			retry := false
			for _, id := range ids {
				pending, err := q.qe.IsPending(ctx, id)
				if pending || err != nil {
					q.logger.Warnf("cannot get audit token for id [%s] because the relative transaction is pending, retry at [%d]: with err [%s]", id, i, err)
					if i == q.NumRetries-1 {
						return nil, errors.Errorf("failed to get audit tokens, tx [%s] is still pending", id.TxId)
					}
					time.Sleep(q.RetryDelay)
					retry = true

					break
				}
			}

			if retry {
				tokens = nil

				continue
			}

			return nil, errors.Wrapf(err, "failed to get audit tokens")
		}

		// The execution was successful, we can stop
		break
	}

	return tokens, nil
}

func (q *QueryEngine) ListHistoryIssuedTokens(ctx context.Context) (*token.IssuedTokens, error) {
	return q.qe.ListHistoryIssuedTokens(ctx)
}

// PublicParams returns the public parameters stored in the vault
func (q *QueryEngine) PublicParams(ctx context.Context) ([]byte, error) {
	return q.qe.PublicParams(ctx)
}

// GetTokens returns the tokens stored in the vault matching the given ids
func (q *QueryEngine) GetTokens(ctx context.Context, inputs ...*token.ID) ([]*token.Token, error) {
	tokens, err := q.qe.GetTokens(ctx, inputs...)

	return tokens, err
}

// GetStatus returns the status of the passed transaction
func (q *QueryEngine) GetStatus(ctx context.Context, txID string) (TxStatus, string, error) {
	return q.qe.GetStatus(ctx, txID)
}

// GetTokenOutputs retrieves the token output as stored on the ledger for the passed ids.
// For each id, the callback is invoked to unmarshal the output
func (q *QueryEngine) GetTokenOutputs(ctx context.Context, ds []*token.ID, f func(id *token.ID, tokenRaw []byte) error) error {
	return q.qe.GetTokenOutputs(ctx, ds, f)
}

// WhoDeletedTokens returns info about who deleted the passed tokens.
// The bool array is an indicator used to tell if the token at a given position has been deleted or not
func (q *QueryEngine) WhoDeletedTokens(ctx context.Context, iDs ...*token.ID) ([]string, []bool, error) {
	return q.qe.WhoDeletedTokens(ctx, iDs...)
}

func (q *QueryEngine) UnspentLedgerTokensIteratorBy(ctx context.Context) (driver.LedgerTokensIterator, error) {
	return q.qe.UnspentLedgerTokensIteratorBy(ctx)
}

type CertificationStorage struct {
	c driver.CertificationStorage
}

func (c *CertificationStorage) Exists(ctx context.Context, id *token.ID) bool {
	if c == nil || c.c == nil {
		return false
	}

	return c.c.Exists(ctx, id)
}

func (c *CertificationStorage) Store(ctx context.Context, certifications map[*token.ID][]byte) error {
	if c == nil || c.c == nil {
		return errors.New("certification storage is not supported")
	}

	return c.c.Store(ctx, certifications)
}

// Vault models a token vault
type Vault struct {
	v                    driver.Vault
	logger               logging.Logger
	certificationStorage *CertificationStorage
}

// NewQueryEngine returns a new query engine
func (v *Vault) NewQueryEngine() *QueryEngine {
	return NewQueryEngine(v.logger, v.v.QueryEngine(), 3, 3*time.Second)
}

func (v *Vault) CertificationStorage() *CertificationStorage {
	return v.certificationStorage
}

// UnspentTokensIterator models an iterator over all unspent tokens stored in the vault
type UnspentTokensIterator struct {
	driver.UnspentTokensIterator
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

const MaxRetry = 3

//go:generate counterfeiter -o mock/transaction_db.go -fake-name TransactionDB . transactionDB
type transactionDB interface {
	NewTransaction() (dbdriver.TransactionStoreTransaction, error)
	GetTokenRequest(ctx context.Context, txID string) ([]byte, error)
	SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error
}

// tokenRequestHasher defines the interface for processing token requests from raw bytes
//
//go:generate counterfeiter -o mock/token_request_hasher.go -fake-name TokenRequestHasher . tokenRequestHasher
type tokenRequestHasher interface {
	ProcessTokenRequest(ctx context.Context, tokenRequestRaw []byte) (tr *token.Request, msgToSign []byte, err error)
}

// tokensService defines the interface for token service operations needed by the listener
//
//go:generate counterfeiter -o mock/tokens_service.go -fake-name TokensService . tokensService
type tokensService interface {
	GetCachedTokenRequest(txID string) (*token.Request, []byte)
	AppendValid(ctx context.Context, tx dbdriver.Transaction, anchor token.RequestAnchor, tr *token.Request) error
}

type Listener struct {
	logger      logging.Logger
	net         dep.Network
	namespace   string
	hasher      tokenRequestHasher
	ttxDB       transactionDB
	tokens      tokensService
	tracer      trace.Tracer
	metrics     *Metrics
	retryRunner utils.RetryRunner
}

func NewListener(
	logger logging.Logger,
	net dep.Network,
	namespace string,
	hasher tokenRequestHasher,
	ttxDB transactionDB,
	tokens tokensService,
	tracer trace.Tracer,
	metricsProvider metrics.Provider,
) *Listener {
	return &Listener{
		logger:      logger,
		net:         net,
		namespace:   namespace,
		hasher:      hasher,
		ttxDB:       ttxDB,
		tokens:      tokens,
		tracer:      tracer,
		metrics:     newMetrics(metricsProvider),
		retryRunner: utils.NewRetryRunner(logger, MaxRetry, time.Second, true),
	}
}

// OnError is called when a finality event for txID could not be delivered after all retries.
func (t *Listener) OnError(ctx context.Context, txID string, err error) {
	t.metrics.RetryExhausted.Add(1)
	t.logger.Errorf("finality listener: all retries exhausted for tx [%s]: %v", txID, err)
}

func (t *Listener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	start := time.Now()
	newCtx, span := t.tracer.Start(ctx, "on_status")
	defer span.End()
	if err := t.retryRunner.RunWithContext(newCtx, func() error {
		err := t.runOnStatus(newCtx, txID, status, message, tokenRequestHash)
		if err != nil {
			t.logger.Errorf("finality listener on [%s] failed with error: [%+v], retrying...", txID, err)
		}

		return err
	}); err != nil {
		t.logger.Errorf("finality listener on [%s] failed with error: [%+v], stop.", txID, err)
	}
	t.metrics.OnStatusDuration.Observe(time.Since(start).Seconds())
}

func (t *Listener) runOnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) error {
	t.logger.DebugfContext(ctx, "tx status changed for tx [%s]: [%s]", txID, status)
	var txStatus storage.TxStatus
	switch status {
	case network.Valid:
		txStatus = storage.Confirmed
		t.logger.DebugfContext(ctx, "get token request for [%s]", txID)

		tr, msgToSign := t.tokens.GetCachedTokenRequest(txID)
		if tr == nil {
			// load it
			tokenRequestRaw, err := t.ttxDB.GetTokenRequest(ctx, txID)
			if err != nil {
				t.logger.ErrorfContext(ctx, "failed retrieving token request [%s]: [%s]", txID, err)

				return errors.Errorf("failed retrieving token request [%s]: [%w]", txID, err)
			}
			t.logger.DebugfContext(ctx, "Read token request")

			// Process token request using the hasher
			tr, msgToSign, err = t.hasher.ProcessTokenRequest(ctx, tokenRequestRaw)
			if err != nil {
				return errors.Errorf("failed to process token request [%s]: [%w]", txID, err)
			}
		}
		t.logger.DebugfContext(ctx, "Check token request")
		if err := t.checkTokenRequest(txID, msgToSign, tokenRequestHash); err != nil {
			t.logger.ErrorfContext(ctx, "tx [%s], %s", txID, err)
			t.metrics.HashMismatches.Add(1)
			txStatus = storage.Deleted
			message = err.Error()
		} else {
			if err := Commit(ctx, t.logger, t.tokens, t.ttxDB, txID, tr); err != nil {
				t.logger.ErrorfContext(ctx, "tx [%s], %s", txID, err)

				return err
			}
		}
	case network.Invalid:
		txStatus = storage.Deleted
	default:

		return errors.Errorf("listener invoked on [%s] with status [%d], cannot proceed", txID, status)
	}

	// update the status, if here, either txStatus is not Confirmed
	if txStatus != storage.Confirmed {
		if err := t.ttxDB.SetStatus(ctx, txID, txStatus, message); err != nil {
			t.logger.ErrorfContext(ctx, "<message> [%s]: [%s]", txID, err)

			return errors.Errorf("<message> [%s]: [%w]", txID, err)
		}
	}

	if txStatus == storage.Confirmed {
		t.metrics.ConfirmedTransactions.Add(1)
	} else {
		t.metrics.DeletedTransactions.Add(1)
	}
	t.logger.DebugfContext(ctx, "tx status changed for tx [%s]: [%s] done", txID, status)

	return nil
}

func (t *Listener) checkTokenRequest(txID string, trToSign []byte, reference []byte) error {
	if base64.StdEncoding.EncodeToString(reference) != utils.Hashable(trToSign).String() {
		t.logger.Errorf("tx [%s], tr hashes [%s][%s]", txID, base64.StdEncoding.EncodeToString(reference), utils.Hashable(trToSign))
		// no further processing of the tokens of these transactions
		return errors.Errorf(
			"token requests do not match, tr hashes [%s][%s]",
			base64.StdEncoding.EncodeToString(reference),
			utils.Hashable(trToSign),
		)
	}

	return nil
}

func Commit(
	ctx context.Context,
	logger logging.Logger,
	tokens tokensService,
	ttxDB transactionDB,
	txID string,
	tr *token.Request,
) error {
	logger.DebugfContext(ctx, "append valid token request for [%s]", txID)
	tx, err := ttxDB.NewTransaction()
	if err != nil {
		logger.ErrorfContext(ctx, "failed creating new transaction [%s]: [%s]", txID, err)

		return errors.Wrapf(err, "failed creating new transaction [%s]", txID)
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()

	if err := tokens.AppendValid(ctx, tx, token.RequestAnchor(txID), tr); err != nil {
		logger.ErrorfContext(ctx, "failed to append valid token request to token db [%s]: [%s]", txID, err)

		return errors.Wrapf(err, "failed to append valid token request to token db [%s]", txID)
	}
	logger.DebugfContext(ctx, "successfully appended valid token request for [%s]", txID)

	if err := tx.SetStatus(ctx, txID, driver.Confirmed, ""); err != nil {
		logger.ErrorfContext(ctx, "failed to set status [%s]: [%s]", txID, err)

		return errors.Wrapf(err, "failed to set status [%s]", txID)
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrapf(err, "failed commit [%s]", txID)
	}

	tx = nil

	return nil
}

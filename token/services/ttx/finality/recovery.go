/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"encoding/base64"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"go.opentelemetry.io/otel/trace"
)

// networkService defines the subset of function of the network service needed by the recovery handler.
//
//go:generate counterfeiter -o mock/network.go -fake-name Network . networkService
type networkService interface {
	GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error)
}

// TTXRecoveryHandler implements transaction recovery by directly querying transaction status
// and applying finality logic synchronously
type TTXRecoveryHandler struct {
	logger        logging.Logger
	network       networkService
	namespace     string
	hasher        tokenRequestHasher
	tmsID         token.TMSID
	transactionDB transactionDB
	tokens        tokensService
	tracer        trace.Tracer
	metrics       *Metrics
}

// NewTTXRecoveryHandler creates a new recovery handler with all dependencies needed
// to perform synchronous transaction recovery
func NewTTXRecoveryHandler(
	logger logging.Logger,
	network networkService,
	namespace string,
	hasher tokenRequestHasher,
	tmsID token.TMSID,
	transactionDB transactionDB,
	tokens tokensService,
	tracer trace.Tracer,
	metricsProvider metrics.Provider,
) *TTXRecoveryHandler {
	return &TTXRecoveryHandler{
		logger:        logger,
		network:       network,
		namespace:     namespace,
		hasher:        hasher,
		tmsID:         tmsID,
		transactionDB: transactionDB,
		tokens:        tokens,
		tracer:        tracer,
		metrics:       NewMetrics(metricsProvider),
	}
}

// Recover attempts to recover a transaction by querying its status and applying finality logic.
// This is a synchronous operation that does not use listeners or retries.
func (h *TTXRecoveryHandler) Recover(ctx context.Context, txID string) error {
	h.logger.Debugf("recovering transaction [%s] in namespace [%s]", txID, h.namespace)

	// Get transaction status from the network
	status, tokenRequestHash, message, err := h.network.GetTransactionStatus(ctx, h.namespace, txID)
	if err != nil {
		return errors.Wrapf(err, "failed to get transaction status for [%s]", txID)
	}

	h.logger.Debugf("transaction [%s] has status [%d] with message [%s]", txID, status, message)

	// Apply finality logic based on status
	return h.applyFinalityLogic(ctx, txID, status, message, tokenRequestHash)
}

// applyFinalityLogic implements the same logic as the finality listener's runOnStatus method
func (h *TTXRecoveryHandler) applyFinalityLogic(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) error {
	h.logger.DebugfContext(ctx, "applying finality logic for tx [%s] with status [%d]", txID, status)

	var txStatus storage.TxStatus
	switch status {
	case network.Valid:
		txStatus = storage.Confirmed
		h.logger.DebugfContext(ctx, "transaction [%s] is valid, processing token request", txID)

		// Get token request
		tr, msgToSign := h.tokens.GetCachedTokenRequest(txID)
		if tr == nil {
			// Load from database
			tokenRequestRaw, err := h.transactionDB.GetTokenRequest(ctx, txID)
			if err != nil {
				h.logger.ErrorfContext(ctx, "failed retrieving token request [%s]: [%s]", txID, err)

				return errors.Wrapf(err, "failed retrieving token request [%s]", txID)
			}

			h.logger.DebugfContext(ctx, "loaded token request from database for [%s]", txID)

			// Process token request using the hasher
			tr, msgToSign, err = h.hasher.ProcessTokenRequest(ctx, tokenRequestRaw)
			if err != nil {
				return errors.Wrapf(err, "failed to process token request [%s]", txID)
			}
		}

		// Verify token request hash
		h.logger.DebugfContext(ctx, "verifying token request hash for [%s]", txID)
		if err := h.checkTokenRequest(txID, msgToSign, tokenRequestHash); err != nil {
			h.logger.ErrorfContext(ctx, "tx [%s], %s", txID, err)
			h.metrics.HashMismatches.Add(1)
			txStatus = storage.Deleted
			message = err.Error()
		} else {
			if err := Commit(ctx, h.logger, h.tokens, h.transactionDB, txID, tr); err != nil {
				h.logger.ErrorfContext(ctx, "tx [%s], %s", txID, err)

				return err
			}
		}

	case network.Invalid:
		txStatus = storage.Deleted
		h.logger.DebugfContext(ctx, "transaction [%s] is invalid", txID)

	default:
		// Transaction is not yet finalized (Busy or Unknown status)
		// This is a normal transient state - don't treat as error to avoid unnecessary claim churn
		h.logger.Debugf("transaction [%s] has status [%d], not yet finalized - will retry on next scan", txID, status)

		// Return nil to release claim gracefully without error
		// The transaction will be picked up again on the next scan after TTL expires
		return nil
	}

	// Update transaction status in database
	if txStatus != storage.Confirmed {
		if err := h.transactionDB.SetStatus(ctx, txID, txStatus, message); err != nil {
			h.logger.ErrorfContext(ctx, "failed to set status for [%s]: [%s]", txID, err)

			return errors.Wrapf(err, "failed to set status for [%s]", txID)
		}
	}

	// Update metrics
	if txStatus == storage.Confirmed {
		h.metrics.ConfirmedTransactions.Add(1)
	} else {
		h.metrics.DeletedTransactions.Add(1)
	}

	h.logger.DebugfContext(ctx, "successfully recovered transaction [%s] with status [%s]", txID, txStatus)

	return nil
}

// checkTokenRequest verifies that the token request hash matches the reference hash from the ledger
func (h *TTXRecoveryHandler) checkTokenRequest(txID string, trToSign []byte, reference []byte) error {
	if base64.StdEncoding.EncodeToString(reference) != utils.Hashable(trToSign).String() {
		h.logger.Errorf("tx [%s], tr hashes [%s][%s]", txID, base64.StdEncoding.EncodeToString(reference), utils.Hashable(trToSign))

		return errors.Errorf(
			"token requests do not match, tr hashes [%s][%s]",
			base64.StdEncoding.EncodeToString(reference),
			utils.Hashable(trToSign),
		)
	}

	return nil
}

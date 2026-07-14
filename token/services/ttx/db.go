/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"go.opentelemetry.io/otel/trace"
)

type (
	// TransactionRecord is a more finer-grained version of a movement record.
	// Given a Token Transaction, for each token action in the Token Request,
	// a transaction record is created for each unique enrollment ID found in the outputs.
	// The transaction record contains the total amount of the token type that was transferred to/from that enrollment ID
	// in that action.
	TransactionRecord = db.TransactionRecord
)

const txIdLabel tracing.LabelName = "tx_id"

type CheckService interface {
	Check(ctx context.Context) ([]string, error)
}

// Service is the interface for the owner service
type Service struct {
	networkProvider dep.NetworkProvider
	tmsID           token.TMSID
	ttxStoreService *ttxdb.StoreService
	tokensService   *tokens.Service
	tmsProvider     dep.TokenManagementServiceProvider
	finalityTracer  trace.Tracer
	metricsProvider metrics.Provider
	checkService    CheckService
}

// Append adds the passed transaction to the database
func (a *Service) Append(ctx context.Context, tx *Transaction) error {
	// append request to the db
	if err := a.ttxStoreService.AppendTransactionRecord(ctx, tx.Request()); err != nil {
		return errors.WithMessagef(err, "failed appending request %s", tx.ID())
	}

	// listen to events
	net, err := a.networkProvider.GetNetwork(tx.Network(), tx.Channel())
	if err != nil {
		return errors.WithMessagef(err, "failed getting network instance for [%s:%s]", tx.Network(), tx.Channel())
	}
	logger.DebugfContext(ctx, "register tx status listener for tx [%s:%s] at network", tx.ID(), tx.Network())

	if err := net.AddFinalityListener(
		tx.Namespace(),
		tx.ID(),
		finality.NewListener(
			logger,
			net,
			tx.Namespace(),
			finality.NewTokenRequestHasher(a.tmsProvider, a.tmsID),
			a.ttxStoreService,
			a.tokensService,
			a.finalityTracer,
			a.metricsProvider,
		),
	); err != nil {
		return errors.WithMessagef(err, "failed listening to network [%s:%s]", tx.Network(), tx.Channel())
	}
	logger.DebugfContext(ctx, "append done for request %s", tx.ID())

	return nil
}

// SetStatus sets the status of the audit records with the passed transaction id to the passed status
func (a *Service) SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error {
	return a.ttxStoreService.SetStatus(ctx, txID, status, message)
}

// GetStatus return the status of the given transaction id.
// It returns an error if no transaction with that id is found
func (a *Service) GetStatus(ctx context.Context, txID string) (TxStatus, string, error) {
	st, sm, err := a.ttxStoreService.GetStatus(ctx, txID)
	if err != nil {
		return Unknown, "", err
	}

	return st, sm, nil
}

// GetTokenRequest returns the token request bound to the passed transaction id, if available.
func (a *Service) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	return a.ttxStoreService.GetTokenRequest(ctx, txID)
}

func (a *Service) AppendTransactionEndorseAck(ctx context.Context, txID string, id view.Identity, sigma []byte) error {
	return a.ttxStoreService.AddTransactionEndorsementAck(ctx, txID, id, sigma)
}

func (a *Service) GetTransactionEndorsementAcks(ctx context.Context, id string) (map[string][]byte, error) {
	return a.ttxStoreService.GetTransactionEndorsementAcks(ctx, id)
}

func (a *Service) Check(ctx context.Context) ([]string, error) {
	return a.checkService.Check(ctx)
}

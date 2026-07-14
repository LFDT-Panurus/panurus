/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type TxOwner struct {
	tms                     dep.TokenManagementService
	owner                   *Service
	transactionInfoProvider *TransactionInfoProvider
}

// NewOwner returns a new owner service.
func NewOwner(sp token.ServiceProvider, tms dep.TokenManagementService) *TxOwner {
	backend := Get(sp, tms)

	return NewTxOwner(tms, backend)
}

// NewTxOwner creates a new TxOwner with the given token management service and backend service.
// This is a lower-level constructor used internally when the backend service is already available.
func NewTxOwner(tms dep.TokenManagementService, backend *Service) *TxOwner {
	return &TxOwner{
		tms:                     tms,
		owner:                   backend,
		transactionInfoProvider: newTransactionInfoProvider(tms, backend),
	}
}

// Append adds a new transaction to the token transaction database.
func (a *TxOwner) Append(ctx context.Context, tx *Transaction) error {
	return a.owner.Append(ctx, tx)
}

// Transactions returns an iterators of transaction records filtered by the given params.
func (a *TxOwner) Transactions(ctx context.Context, params db.QueryTransactionsParams, pagination driver2.Pagination) (*driver2.PageIterator[*storage.TransactionRecord], error) {
	return a.owner.ttxStoreService.Transactions(ctx, params, pagination)
}

// TransactionInfo returns the transaction info for the given transaction ID.
func (a *TxOwner) TransactionInfo(ctx context.Context, txID string) (*TransactionInfo, error) {
	return a.transactionInfoProvider.TransactionInfo(ctx, txID)
}

// SetStatus sets the status of the audit records with the passed transaction id to the passed status
func (a *TxOwner) SetStatus(ctx context.Context, txID string, status storage.TxStatus, message string) error {
	return a.owner.SetStatus(ctx, txID, status, message)
}

// GetStatus return the status of the given transaction id.
// It returns an error if no transaction with that id is found
func (a *TxOwner) GetStatus(ctx context.Context, txID string) (TxStatus, string, error) {
	return a.owner.GetStatus(ctx, txID)
}

// GetTokenRequest retrieves the serialized token request for the given transaction ID.
// Returns an error if the transaction is not found in the database.
func (a *TxOwner) GetTokenRequest(ctx context.Context, txID string) ([]byte, error) {
	return a.owner.GetTokenRequest(ctx, txID)
}

// Check performs a health check on the owner service and returns any issues found.
// It delegates to the underlying service for the check.
func (a *TxOwner) Check(ctx context.Context) ([]string, error) {
	return a.owner.Check(ctx)
}

// appendTransactionEndorseAck records an endorsement acknowledgment signature from a party
// for the given transaction. This is used internally during transaction distribution to
// track which parties have acknowledged receipt of the transaction.
func (a *TxOwner) appendTransactionEndorseAck(ctx context.Context, tx *Transaction, id view.Identity, sigma []byte) error {
	return a.owner.AppendTransactionEndorseAck(ctx, tx.ID(), id, sigma)
}

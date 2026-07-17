/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep/db"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"go.opentelemetry.io/otel/trace"
)

type finalityDB interface {
	AddStatusListener(txID string, ch chan db.TransactionStatusEvent)
	DeleteStatusListener(txID string, ch chan db.TransactionStatusEvent)
	ListenerTxIDs() []string
	NotifyStatus(ctx context.Context, txID string, status TxStatus, message string)
	GetStatus(ctx context.Context, txID string) (TxStatus, string, error)
	GetStatuses(ctx context.Context, txIDs []string) (map[string]TxStatus, error)
}

type finalityView struct {
	pollingTimeout time.Duration
	opts           []TxOption
}

// NewFinalityView returns an instance of the finalityView.
// The view does the following: It waits for the finality of the passed transaction.
// If the transaction is final, the vault is updated.
func NewFinalityView(tx *Transaction, opts ...TxOption) *finalityView {
	return NewFinalityWithOpts(append([]TxOption{WithTransactions(tx)}, opts...)...)
}

func NewFinalityWithOpts(opts ...TxOption) *finalityView {
	pollingTimeout := 1 * time.Second
	options, err := CompileOpts(opts...)
	if err == nil && options.PollingTimeout > 0 {
		pollingTimeout = options.PollingTimeout
	}

	return &finalityView{opts: opts, pollingTimeout: pollingTimeout}
}

// Call executes the view.
// The view does the following: It waits for the finality of the passed transaction.
// If the transaction is final, the vault is updated.
func (f *finalityView) Call(ctx view.Context) (any, error) {
	// Compile options
	options, err := CompileOpts(f.opts...)
	if err != nil {
		return nil, errors.Wrapf(errors.Join(ErrInvalidInput, err), "failed to compile options")
	}
	txID := options.TxID
	tmsID := options.TMSID
	timeout := options.Timeout
	if options.Transaction != nil {
		txID = options.Transaction.ID()
		tmsID = options.Transaction.TMSID()
	}
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	// Zero out any non-needed references to allow the garbage collector to reclaim them
	f.opts = nil
	options.Transaction = nil

	return f.call(ctx, txID, tmsID, timeout)
}

func (f *finalityView) call(ctx view.Context, txID string, tmsID token.TMSID, timeout time.Duration) (any, error) {
	// Validate inputs
	if txID == "" {
		return nil, errors.Wrapf(ErrInvalidInput, "transaction ID cannot be empty")
	}
	if timeout < 0 {
		return nil, errors.Wrapf(ErrInvalidInput, "timeout cannot be negative")
	}
	if timeout > 24*time.Hour {
		return nil, errors.Wrapf(ErrInvalidInput, "timeout cannot exceed 24 hours")
	}

	logger.DebugfContext(ctx.Context(), "Listen to finality of [%s]", txID)

	c := ctx.Context()
	if timeout != 0 {
		var cancel context.CancelFunc
		c, cancel = context.WithTimeout(c, timeout)
		defer cancel()
	}

	transactionDB, err := dep.GetTransactionDB(ctx, tmsID)
	if err != nil {
		return nil, err
	}
	auditDB, err := dep.GetAuditDB(ctx, tmsID)
	if err != nil {
		return nil, err
	}

	// Check if transaction is known in at least one database
	// Note: We check both databases to determine which ones to monitor
	statusTTXDB, _, errTTXDB := transactionDB.GetStatus(ctx.Context(), txID)
	knownInTTXDB := errTTXDB == nil && statusTTXDB != ttxdb.Unknown

	statusAuditDB, _, errAuditDB := auditDB.GetStatus(ctx.Context(), txID)
	knownInAuditDB := errAuditDB == nil && statusAuditDB != ttxdb.Unknown

	if !knownInTTXDB && !knownInAuditDB {
		return nil, errors.Wrapf(ErrTransactionUnknown, "transaction [%s] is unknown for [%s]", txID, tmsID)
	}

	logger.DebugfContext(ctx.Context(), "Listen for DB finality")
	if knownInTTXDB {
		logger.DebugfContext(ctx.Context(), "Request TTXDB finality")
		if err := f.dbFinality(c, txID, transactionDB); err != nil {
			return nil, err
		}
	}
	if knownInAuditDB {
		logger.DebugfContext(ctx.Context(), "Request AuditDB finality")
		if err := f.dbFinality(c, txID, auditDB); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

// dbFinality waits for a transaction to reach finality in a specific database.
// Detection is push-first: SetStatus on the database notifies the registered
// listener channel in-process. A shared per-database poller batch-checks every
// waited-on transaction (pollingTimeout interval) as a fallback for lost push
// events, so no per-transaction polling happens here.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - txID: Transaction ID to monitor
//   - finalityDB: Database to monitor for finality
//
// Returns:
//   - error: Error if transaction is invalid or timeout occurs
func (f *finalityView) dbFinality(ctx context.Context, txID string, finalityDB finalityDB) error {
	// notice that adding the listener can happen after the event we are looking for has already happened
	// therefore we need to check the status right after registering
	dbChannel := make(chan db.TransactionStatusEvent, 1)

	logger.DebugfContext(ctx, "Add status listener")
	finalityDB.AddStatusListener(txID, dbChannel)
	logger.DebugfContext(ctx, "Added status listener")
	defer func() {
		logger.DebugfContext(ctx, "Remove status listener")
		finalityDB.DeleteStatusListener(txID, dbChannel)
		close(dbChannel)
		logger.DebugfContext(ctx, "Removed status listener and closed channel")
	}()

	unregister := registerStatusWaiter(finalityDB, f.pollingTimeout)
	defer unregister()

	logger.DebugfContext(ctx, "Get status")
	status, _, err := finalityDB.GetStatus(ctx, txID)
	if err == nil {
		if status == ttxdb.Confirmed {
			return nil
		}
		if status == ttxdb.Deleted {
			logger.ErrorfContext(ctx, "Deleted tx")

			return errors.Wrapf(ErrFinalityInvalidTransaction, "transaction [%s] is not valid", txID)
		}
	}

	logger.DebugfContext(ctx, "Listen DB channels")
	select {
	case <-ctx.Done():
		logger.ErrorfContext(ctx, "Is [%s] final? Failed to listen to transaction for timeout", txID)

		return errors.Wrapf(ErrFinalityTimeout, "failed to listen to transaction [%s] for timeout", txID)
	case event := <-dbChannel:
		trace.SpanFromContext(ctx).AddLink(trace.LinkFromContext(event.Ctx))
		logger.DebugfContext(ctx, "Got an answer to finality of [%s]: [%s]", txID, event)
		if event.ValidationCode == ttxdb.Confirmed {
			return nil
		}
		logger.ErrorfContext(ctx, "transaction [%s] is not valid [%s]", txID, TxStatusMessage[event.ValidationCode])

		return errors.Wrapf(ErrFinalityInvalidTransaction, "transaction [%s] is not valid [%s]", txID, TxStatusMessage[event.ValidationCode])
	}
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

const finalityTimeout = 10 * time.Minute

type orderingView struct {
	opts []TxOption
}

// NewOrderingView returns a new instance of the orderingView struct.
// The view does the following:
// 1. It broadcasts the token transaction to the proper backend.
func NewOrderingView(tx *Transaction, opts ...TxOption) *orderingView {
	return NewOrderingViewWithOpts(append([]TxOption{WithTransactions(tx)}, opts...)...)
}

// NewOrderingViewWithOpts creates a new ordering view with the provided transaction options.
// This is a lower-level constructor that allows passing options directly without a transaction instance.
func NewOrderingViewWithOpts(opts ...TxOption) *orderingView {
	return &orderingView{opts: opts}
}

// Call execute the view.
// The view does the following:
// 1. It broadcasts the token transaction to the proper backend.
func (o *orderingView) Call(context view.Context) (any, error) {
	start := time.Now()
	// Compile options
	options, err := CompileOpts(o.opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compile options")
	}
	if err := o.broadcast(context, options.Transaction); err != nil {
		return nil, err
	}

	// cache the token request into the tokens db
	t, err := tokens.GetService(context, options.Transaction.TMSID())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get tokens db for [%s]", options.Transaction.TMSID())
	}
	if !options.NoCachingRequest {
		if err := t.CacheRequest(context.Context(), options.Transaction.TokenRequest); err != nil {
			logger.WarnfContext(context.Context(), "failed to cache token request [%s], this might cause delay, investigate when possible: [%s]", options.Transaction.TokenRequest.Anchor, err)
		}
	}

	labels := []string{
		"network", options.Transaction.Network(),
		"channel", options.Transaction.Channel(),
		"namespace", options.Transaction.Namespace(),
	}
	GetMetrics(context).OrderingDuration.With(labels...).Observe(time.Since(start).Seconds())

	return nil, nil
}

// broadcast sends the transaction envelope to the network's ordering service for inclusion in the ledger.
// Returns an error if the transaction is nil, the network is not found, or the broadcast fails.
func (o *orderingView) broadcast(context view.Context, transaction *Transaction) error {
	if transaction == nil {
		return errors.Errorf("transaction is nil")
	}
	nw := network.GetInstance(context, transaction.Network(), transaction.Channel())
	if nw == nil {
		return errors.Errorf("network [%s] not found", transaction.Network())
	}
	if err := nw.Broadcast(context.Context(), transaction.Envelope); err != nil {
		return errors.WithMessagef(err, "failed to broadcast token transaction [%s]", transaction.ID())
	}

	return nil
}

type orderingAndFinalityView struct {
	tx      *Transaction
	timeout time.Duration
}

// NewOrderingAndFinalityView returns a new instance of the orderingAndFinalityView struct.
// The view does the following:
// 1. It broadcasts the token transaction to the proper backend.
// 2. It waits for finality of the token transaction.
// The default timeout is used and it is set to 10mins.
func NewOrderingAndFinalityView(tx *Transaction) *orderingAndFinalityView {
	return NewOrderingAndFinalityWithTimeoutView(tx, finalityTimeout)
}

// NewOrderingAndFinalityWithTimeoutView returns a new instance of the orderingAndFinalityView struct.
// The view does the following:
// 1. It broadcasts the token transaction to the proper backend.
// 2. It waits for finality of the token transaction.
func NewOrderingAndFinalityWithTimeoutView(tx *Transaction, timeout time.Duration) *orderingAndFinalityView {
	return &orderingAndFinalityView{tx: tx, timeout: timeout}
}

// Call executes the view.
// The view does the following:
// 1. It broadcasts the token transaction to the proper backend.
// 2. It waits for finality of the token transaction.
// It returns in case the operation is not completed before the passed timeout.
func (o *orderingAndFinalityView) Call(ctx view.Context) (any, error) {
	if _, err := ctx.RunView(NewOrderingView(o.tx)); err != nil {
		return nil, err
	}

	return ctx.RunView(NewFinalityView(o.tx, WithTimeout(o.timeout)))
}

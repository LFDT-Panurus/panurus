/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc

import (
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// NewCollectEndorsementsView returns an instance of the ttx collectEndorsementsView struct
func NewCollectEndorsementsView(tx *Transaction) view.View {
	return ttx.NewCollectEndorsementsView(tx.Transaction)
}

type receiveTransactionView struct {
	network string
	channel string
}

// NewReceiveTransactionView returns an instance of receiveTransactionView struct
func NewReceiveTransactionView(network string) *receiveTransactionView {
	return &receiveTransactionView{network: network}
}

func (f *receiveTransactionView) Call(context view.Context) (any, error) {
	// Wait to receive a transaction back
	ch := context.Session().Receive()

	select {
	case msg := <-ch:
		if msg == nil {
			return nil, errors.Errorf("receive nil msg")
		}
		if msg.Status == view.ERROR {
			return nil, errors.New(string(msg.Payload))
		}
		// The transaction is distributed by ttx.CollectEndorsements, which now
		// wraps it in a versioned envelope carrying a TransactionPayload.
		env, err := jsession.UnwrapEnvelope(msg.Payload, ttx.TypeTransaction)
		if err != nil {
			return nil, errors.Wrap(err, "failed unwrapping transaction envelope")
		}
		var payload ttx.TransactionPayload
		if err := json.Unmarshal(env.Body, &payload); err != nil {
			return nil, errors.Wrap(err, "failed unmarshalling transaction payload")
		}
		tx, err := NewTransactionFromBytes(context, f.network, f.channel, payload.Raw)
		if err != nil {
			return nil, err
		}

		return tx, nil
	case <-time.After(240 * time.Second):
		return nil, errors.New("timeout reached")
	}
}

// ReceiveTransaction executes the receiveTransactionView and returns the received transaction
func ReceiveTransaction(context view.Context) (*Transaction, error) {
	logger.DebugfContext(context.Context(), "receive a new transaction...")

	txBoxed, err := context.RunView(NewReceiveTransactionView(""))
	if err != nil {
		return nil, err
	}

	cctx, ok := txBoxed.(*Transaction)
	if !ok {
		return nil, errors.Errorf("received transaction of wrong type [%T]", cctx)
	}
	logger.DebugfContext(context.Context(), "received transaction with id [%s]", cctx.ID())

	return cctx, nil
}

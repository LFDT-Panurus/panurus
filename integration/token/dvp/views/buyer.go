/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"github.com/LFDT-Panurus/panurus/integration/token/dvp/views/house"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type BuyHouseView struct{}

func (b *BuyHouseView) Call(context view.Context) (any, error) {
	// Respond to a request for an identity to transfer the house
	meHouse, err := nfttx.RespondRequestRecipientIdentity(context)
	assert.NoError(err, "failed to respond to identity request")

	// Respond to a request to exchange identifies for the cash transfer
	meCash, otherCash, err := ttx.RespondExchangeRecipientIdentities(context)
	assert.NoError(err, "failed getting identity")

	// Receive the request to transfer action
	tx, action, err := ttx.ReceiveAction(context)
	assert.NoError(err, "failed receiving action")

	// check transaction, it must contain the house transfer
	nfttx := nfttx.Wrap(tx)
	outputs, err := nfttx.Outputs()
	assert.NoError(err, "failed getting outputs")
	assert.NoError(outputs.Validate(), "failed validating outputs")
	assert.True(outputs.Count() == 1, "the transaction must contain one output")
	assert.True(outputs.ByRecipient(meHouse).Count() == 1, "the transaction must contain one output that names the recipient")
	house := &house.House{}
	assert.NoError(outputs.StateAt(0, house), "failed to get house state")
	assert.NotEmpty(house.LinearID, "the house must have a linear ID")
	assert.True(house.Valuation > 0, "the house must have a valuation")
	assert.NotEmpty(house.Address, "the house must have an address")

	// check action
	assert.Equal(otherCash, action.Recipient, "recipient mismatch")
	assert.True(action.Amount > 0, "amount must be greater than 0")
	assert.Equal(token2.Type("USD"), action.Type, "currency must be USD")
	assert.Equal(meCash, action.From, "sender mismatch")

	// check house and action match
	assert.Equal(house.Valuation, action.Amount, "valuation mismatch")

	// Append the cash transfer to the transaction
	err = tx.Transfer(
		ttx.MyWalletFromTx(context, tx),
		action.Type,
		[]uint64{action.Amount},
		[]view.Identity{action.Recipient},
	)
	assert.NoError(err, "failed appending transfer")

	// Respond to the request to transfer the cash
	_, err = context.RunView(ttx.NewCollectActionsResponderView(tx, action))
	assert.NoError(err, "failed responding to action collect")

	// Sign and send back
	_, err = context.RunView(ttx.NewEndorseView(tx))
	assert.NoError(err, "failed to endorse transaction")

	// Wait for confirmation
	return context.RunView(ttx.NewFinalityView(tx))
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package house

import (
	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type AcceptHouseView struct{}

func (a *AcceptHouseView) Call(context view.Context) (any, error) {
	// The recipient of a token (issued or transfer) responds, as first operation,
	// to a request for a recipient.
	// The recipient can do that by using the following code.
	// The recipient identity will be taken from the default wallet (ttx.MyWallet(context)), if not otherwise specified.
	id, err := nfttx.RespondRequestRecipientIdentity(context)
	assert.NoError(err, "failed to respond to identity request")

	// At some point, the recipient receives the token transaction that in the mean time has been assembled
	tx, err := nfttx.ReceiveTransaction(context)
	assert.NoError(err, "failed to receive tokens")

	// The recipient can perform any check on the transaction as required by the business process
	// In particular, here, the recipient checks that the transaction contains one output that names the recipient.
	// (The recipient is receiving something)
	outputs, err := tx.Outputs()
	assert.NoError(err, "failed getting outputs")
	assert.NoError(outputs.Validate(), "failed validating outputs")
	assert.True(outputs.Count() == 1, "the transaction must contain one output")
	assert.True(outputs.ByRecipient(id).Count() == 1, "the transaction must contain one output that names the recipient")
	house := &House{}
	assert.NoError(outputs.StateAt(0, house), "failed to get house state")
	assert.NotEmpty(house.LinearID, "the house must have a linear ID")
	assert.True(house.Valuation > 0, "the house must have a valuation")
	assert.NotEmpty(house.Address, "the house must have an address")

	// If everything is fine, the recipient accepts and sends back her signature.
	// Notice that, a signature from the recipient might or might not be required to make the transaction valid.
	// This depends on the driver implementation.
	_, err = context.RunView(nfttx.NewAcceptView(tx))
	assert.NoError(err, "failed to accept new tokens")

	// Before completing, the recipient waits for finality of the transaction
	_, err = context.RunView(nfttx.NewFinalityView(tx))
	assert.NoError(err, "new tokens were not committed")

	return nil, nil
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"math/big"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type AcceptCashView struct{}

func (a *AcceptCashView) Call(context view.Context) (any, error) {
	// The recipient of a token (issued or transfer) responds, as first operation,
	// to a request for a recipient.
	// The recipient can do that by using the following code.
	// The recipient identity will be taken from the default wallet (ttx.MyWallet(context)), if not otherwise specified.
	id, err := ttx.RespondRequestRecipientIdentityUsingWallet(context, "")
	assert.NoError(err, "failed to respond to identity request")

	// At some point, the recipient receives the token transaction that in the meantime has been assembled
	tx, err := ttx.ReceiveTransaction(context)
	assert.NoError(err, "failed to receive tokens")

	// The recipient can perform any check on the transaction as required by the business process
	// In particular, here, the recipient checks that the transaction contains at least one output, and
	// that there is at least one output that names the recipient.(The recipient is receiving something).
	outputs, err := tx.Outputs()
	assert.NoError(err, "failed getting outputs")
	assert.True(outputs.Count() > 0)
	assert.True(outputs.ByRecipient(id).Count() > 0)

	// The recipient here is checking that, for each type of token she is receiving,
	// she does not hold already more than 3000 units of that type.
	// Just a fancy query to show the capabilities of the services we are using.
	for _, output := range outputs.ByRecipient(id).Outputs() {
		if output.Type == "MAX" {
			continue
		}
		balance, err := ttx.MyWallet(context, token.WithTMSID(tx.TMSID())).Balance(context.Context(), ttx.WithType(output.Type))
		assert.NoError(err, "failed retrieving balance for type [%s]", output.Type)
		assert.True(balance.Cmp(big.NewInt(3000)) <= 0, "cannot have more than 3000 unspent quantity for type [%s]", output.Type)
	}

	// If everything is fine, the recipient accepts and sends back her signature.
	// Notice that, a signature from the recipient might or might not be required to make the transaction valid.
	// This depends on the driver implementation.
	_, err = context.RunView(ttx.NewAcceptView(tx))
	assert.NoError(err, "failed to accept new tokens")

	// Sanity checks:
	// - the transaction is in pending state
	owner := ttx.NewOwner(context, tx.TokenService())
	vc, _, err := owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Pending, vc, "transaction [%s] should be in busy state", tx.ID())

	// Before completing, the recipient waits for finality of the transaction
	_, err = context.RunView(ttx.NewFinalityView(tx))
	assert.NoError(err, "new tokens were not committed")

	// Sanity checks:
	// - the transaction is in confirmed state
	vc, _, err = owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Confirmed, vc, "transaction [%s] should be in valid state", tx.ID())

	// Check that the tokens are or are not in the db
	AssertTokens(context, tx, outputs, id)

	return nil, nil
}

type AcceptPreparedCashView struct {
	WaitFinality bool
}

func (a *AcceptPreparedCashView) Call(context view.Context) (any, error) {
	// The recipient of a token (issued or transfer) responds, as first operation,
	// to a request for a recipient.
	// The recipient can do that by using the following code.
	// The recipient identity will be taken from the default wallet (ttx.MyWallet(context)), if not otherwise specified.
	id, err := ttx.RespondRequestRecipientIdentityUsingWallet(context, "")
	assert.NoError(err, "failed to respond to identity request")

	// At some point, the recipient receives the token transaction that in the meantime has been assembled
	tx, err := ttx.ReceiveTransaction(context)
	assert.NoError(err, "failed to receive tokens")

	// The recipient can perform any check on the transaction as required by the business process
	// In particular, here, the recipient checks that the transaction contains at least one output, and
	// that there is at least one output that names the recipient (The recipient is receiving something).
	outputs, err := tx.Outputs()
	assert.NoError(err, "failed getting outputs")
	assert.True(outputs.Count() > 0)
	assert.True(outputs.ByRecipient(id).Count() > 0)

	// If everything is fine, the recipient accepts and sends back her signature.
	// Notice that, a signature from the recipient might or might not be required to make the transaction valid.
	// This depends on the driver implementation.
	_, err = context.RunView(ttx.NewAcceptView(tx))
	assert.NoError(err, "failed to accept new tokens")

	return nil, nil
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	view4 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type Withdrawal struct {
	// TMSID the token management service identifier
	TMSID token.TMSID
	// Wallet of the recipient of the cash to be issued
	Wallet string
	// Amount represent the number of units of a certain token type stored in the token
	Amount uint64
	// TokenType is the type of token to issue
	TokenType token2.Type
	// Issuer identifies the issuer
	Issuer string
	// Recipient information
	RecipientData *token.RecipientData
	// NotAnonymous true if the transaction must be anonymous, false otherwise
	NotAnonymous bool
}

type WithdrawalInitiatorView struct {
	*Withdrawal
}

func (i *WithdrawalInitiatorView) Call(context view.Context) (any, error) {
	// First the initiator send a withdrawal request to the issuer.
	// If the initiator has already some recipient data, it uses that directly
	var id view.Identity
	var session view.Session
	var err error
	if i.RecipientData != nil {
		// Use the passed RecipientData. RequestWithdrawalForRecipient validates and
		// registers it locally against the wallet before using it.
		// We have an external wallet here.
		// We need to provide access to it to RequestWithdrawalForRecipientView.
		// Indeed, a signature is needed to prove to the issuer that the caller owns the recipient data
		stream := view4.GetStream(context)
		// Then request withdrawal
		externalWalletSignerServer := ttx.NewStreamExternalWalletSignerServer(stream)
		defer func() {
			assert.NoError(externalWalletSignerServer.Done())
		}()
		id, session, err = ttx.RequestWithdrawalForRecipient(
			context,
			view.Identity(i.Issuer),
			i.Wallet,
			i.TokenType,
			i.Amount,
			i.NotAnonymous,
			i.RecipientData,
			token.WithTMSID(i.TMSID),
			ttx.WithExternalWalletSigner(i.Wallet, externalWalletSignerServer),
		)
	} else {
		id, session, err = ttx.RequestWithdrawal(context, view.Identity(i.Issuer), i.Wallet, i.TokenType, i.Amount, i.NotAnonymous, token.WithTMSID(i.TMSID))
	}
	// Request withdrawal
	assert.NoError(err, "failed to send withdrawal request")

	// At this point we have an inversion of roles.
	// The initiator becomes a responder.
	// This is a trick to the reuse the same API independently of the role a party plays.
	return context.RunView(nil, view.AsResponder(session), view.WithViewCall(
		func(context view.Context) (any, error) {
			// At some point, the recipient receives the token transaction that in the meantime has been assembled
			tx, err := ttx.ReceiveTransaction(context)
			assert.NoError(err, "failed to receive tokens")

			// The recipient can perform any check on the transaction as required by the business process
			// In particular, here, the recipient checks that the transaction contains at least one output, and
			// that there is at least one output that names the recipient.(The recipient is receiving something).
			outputs, err := tx.Outputs()
			assert.NoError(err, "failed getting outputs")
			assert.True(outputs.Count() > 0, "expected at least one output")
			assert.True(outputs.ByRecipient(id).Count() > 0, "expected at least one output assigned to [%s]", id)
			actualAmount := outputs.ByRecipient(id).Sum().Uint64()
			assert.True(actualAmount == i.Amount, "expected outputs to sum to [%d], got [%d]", i.Amount, actualAmount)

			// If everything is fine, the recipient accepts and sends back her signature.
			// Notice that, a signature from the recipient might or might not be required to make the transaction valid.
			// This depends on the driver implementation.
			_, err = context.RunView(ttx.NewAcceptView(tx))
			assert.NoError(err, "failed to accept new tokens")

			// Before completing, the recipient waits for finality of the transaction
			_, err = context.RunView(ttx.NewFinalityView(tx, ttx.WithTimeout(1*time.Minute)))
			assert.NoError(err, "new tokens were not committed")

			return tx.ID(), nil
		},
	))
}

type WithdrawalInitiatorViewFactory struct{}

func (p *WithdrawalInitiatorViewFactory) NewView(in []byte) (view.View, error) {
	f := &WithdrawalInitiatorView{Withdrawal: &Withdrawal{}}
	err := json.Unmarshal(in, f.Withdrawal)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type WithdrawalResponderView struct {
	Auditor string
}

func (p *WithdrawalResponderView) Call(context view.Context) (any, error) {
	// First the issuer receives the withdrawal request
	issueRequest, err := ttx.ReceiveWithdrawalRequest(context)
	assert.NoError(err, "failed to receive withdrawal request")

	// Now we have an inversion of roles. The issuer becomes an initiator.
	// This is a trick to reuse the code used in IssueCashView
	return context.RunView(nil, view.AsInitiator(), view.WithViewCall(func(context view.Context) (any, error) {
		// Before assembling the transaction, the issuer can perform any activity that best fits the business process.
		// In this example, if the token type is USD, the issuer checks that no more than 230 units of USD
		// have been issued already including the current request.
		// No check is performed for other types.
		tms, err := token.GetManagementService(context, token.WithTMSID(issueRequest.TMSID))
		assert.NoError(err, "failed getting management service")
		wallet, err := tms.WalletManager().IssuerWallet(context.Context(), "")
		assert.NoError(err, "issuer wallet not found")

		// At this point, the issuer is ready to prepare the token transaction.
		// The issuer creates a new token transaction and specifies the auditor that must be contacted to approve the operation.
		var tx *ttx.Transaction
		var auditorID string
		if len(p.Auditor) == 0 {
			assert.NoError(GetKVS(context).Get(context.Context(), "auditor", &auditorID), "failed to retrieve auditor id")
		} else {
			auditorID = p.Auditor
		}
		idProvider, err := id.GetProvider(context)
		assert.NoError(err, "failed getting id provider")
		auditor := idProvider.Identity(auditorID)
		if !issueRequest.NotAnonymous {
			// The issuer creates an anonymous transaction (this means that the resulting Fabric transaction will be signed using idemix, for example),
			tx, err = ttx.NewAnonymousTransaction(context, ttx.WithAuditor(auditor), ttx.WithTMSID(issueRequest.TMSID))
		} else {
			// The issuer creates a nominal transaction using the default identity
			tx, err = ttx.NewTransaction(context, nil, ttx.WithAuditor(auditor), ttx.WithTMSID(issueRequest.TMSID))
		}
		assert.NoError(err, "failed creating issue transaction")

		// The issuer adds a new issue operation to the transaction following the instruction received
		err = tx.Issue(
			wallet,
			issueRequest.RecipientData.Identity,
			issueRequest.TokenType,
			issueRequest.Amount,
		)
		assert.NoError(err, "failed adding new issued token")

		// The issuer is ready to collect all the required signatures.
		// In this case, the issuer's and the auditor's signatures.
		// Invoke the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
		// This is all done in one shot running the following view.
		// Before completing, all recipients receive the approved transaction.
		// Depending on the token driver implementation, the recipient's signature might or might not be needed to make
		// the token transaction valid.
		_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
		assert.NoError(err, "failed to sign issue transaction for "+tx.ID())

		// Last but not least, the issuer sends the transaction for ordering and waits for transaction finality.
		_, err = context.RunView(ttx.NewOrderingAndFinalityWithTimeoutView(tx, 1*time.Minute))
		assert.NoError(err, "failed to commit issue transaction")

		return tx.ID(), nil
	}))
}

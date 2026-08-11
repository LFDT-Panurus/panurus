/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/multisig"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// MultiSigLock contains the input information for a multisig-based escrow
type MultiSigLock struct {
	// Auditor is the name of the auditor that must be contacted to approve the operation
	Auditor string
	// Wallet is the identifier of the wallet that owns the tokens to transfer
	Wallet string
	// Type of tokens to transfer
	Type token.Type
	// Amount to transfer
	Amount uint64
	// MultisigIdentities contains the identities of the FSC nodes of the multisig escrow
	MultisigIdentities []view.Identity
	// MultisigEIDs contains the expected enrolment ids of the multisig escrow
	MultisigEIDs []string
	// The TMS to pick in case of multiple TMSIDs
	TMSID *token2.TMSID
	// NotAnonymous true if the transaction must be anonymous, false otherwise
	NotAnonymous bool
}

type MultiSigLockView struct {
	*MultiSigLock
}

func (lv *MultiSigLockView) Call(context view.Context) (txID any, err error) {
	// As a first step operation, the sender contacts the escrow FSC nodes
	// to ask for the identity to use to assign ownership of the freshly created token.
	// Notice that, this step would not be required if the sender knew already which
	// identity the escrow wants to use.
	recipient, err := multisig.RequestRecipientIdentity(context, lv.MultisigIdentities, token2.WithTMSIDPointer(lv.TMSID))
	assert.NoError(err, "failed requesting recipients")

	// The sender will select tokens owned by this wallet
	senderWallet := ttx.GetWallet(context, lv.Wallet, token2.WithTMSIDPointer(lv.TMSID))
	assert.NotNil(senderWallet, "sender wallet [%s] not found", lv.Wallet)

	// At this point, the sender is ready to prepare the token transaction.
	// If NotAnonymous == false, then the sender creates an anonymous transaction (this means that the resulting Fabric transaction will be signed using idemix, for example),
	// and specify the auditor that must be contacted to approve the operation.
	var tx *ttx.Transaction
	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	txOpts := []ttx.TxOption{
		ttx.WithTMSIDPointer(lv.TMSID),
		ttx.WithAuditor(idProvider.Identity(lv.Auditor)),
		ttx.WithAnonymousTransaction(!lv.NotAnonymous),
	}
	tx, err = ttx.NewTransaction(context, nil, txOpts...)
	assert.NoError(err, "failed creating transaction")

	// lock
	err = multisig.Wrap(tx).Lock(senderWallet, lv.Type, lv.Amount, recipient)
	assert.NoError(err, "failed adding transfer action [%d:%v]", lv.Amount, recipient)

	_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to sign transaction [<<<%s>>>]", tx.ID())

	// Sanity checks:
	// - the transaction is in pending state
	owner := ttx.NewOwner(context, tx.TokenService())
	vc, _, err := owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Pending, vc, "transaction [%s] should be in busy state", tx.ID())

	// Send to the ordering service and wait for finality
	_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed asking ordering")

	// Sanity checks:
	// - the transaction is in confirmed state
	vc, _, err = owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Confirmed, vc, "transaction [%s] should be in valid state", tx.ID())

	return tx.ID(), nil
}

type MultiSigLockViewFactory struct{}

func (f *MultiSigLockViewFactory) NewView(in []byte) (view.View, error) {
	v := &MultiSigLockView{MultiSigLock: &MultiSigLock{}}
	err := json.Unmarshal(in, v.MultiSigLock)
	assert.NoError(err, "failed unmarshalling input")

	return v, nil
}

// MultiSigSpend contains the input information to spend a token
type MultiSigSpend struct {
	// Auditor is the name of the auditor that must be contacted to approve the operation
	Auditor string
	// Wallet is the identifier of the wallet that owns the tokens to transfer
	Wallet string
	// MultisigIdentities contains the identities of the FSC nodes of the multisig escrow
	Recipient view.Identity
	// The TMS to pick in case of multiple TMSIDs
	TMSID     *token2.TMSID
	TokenType token.Type
}

type MultiSigSpendView struct {
	*MultiSigSpend
}

func (r *MultiSigSpendView) Call(context view.Context) (res any, err error) {
	serviceOpts := ServiceOpts(r.TMSID)
	recipient, err := ttx.RequestRecipientIdentity(context, r.Recipient, serviceOpts...)
	assert.NoError(err, "failed getting recipient")

	// choose the multisig token to spend
	spendWallet := multisig.GetWallet(context, r.Wallet, serviceOpts...)
	assert.NotNil(spendWallet, "wallet [%s] not found", r.Wallet)

	// TODO: provides more ways to select multisig token
	matched, err := multisig.Wallet(context, spendWallet).ListTokens(context.Context())
	assert.NoError(err, "failed to fetch multisig tokens")
	assert.True(matched.Count() == 1, "expected only one multisig script to match, got [%d]", matched.Count())

	// contact the co-owners about the intention to spend the multisig token
	_, err = context.RunView(multisig.NewRequestSpendView(matched.At(0), serviceOpts...))
	assert.NoError(err, "failed to request spend")

	// generate the transaction
	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	tx, err := ttx.NewAnonymousTransaction(
		context,
		TxOpts(r.TMSID, ttx.WithAuditor(idProvider.Identity(r.Auditor)))...,
	)
	assert.NoError(err, "failed to create an multisig transaction")
	assert.NoError(multisig.Wrap(tx).Spend(spendWallet, matched.At(0), recipient), "failed adding a spend for [%s]", matched.At(0).Id)

	_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to collect endorsements on multisig transaction")

	_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit multisig transaction")

	return tx.ID(), nil
}

type MultiSigSpendViewFactory struct{}

func (p *MultiSigSpendViewFactory) NewView(in []byte) (view.View, error) {
	f := &MultiSigSpendView{MultiSigSpend: &MultiSigSpend{}}
	err := json.Unmarshal(in, f.MultiSigSpend)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type MultiSigAcceptSpendView struct{}

func (m *MultiSigAcceptSpendView) Call(context view.Context) (any, error) {
	// receive the request to spend a multi-sig token
	request, err := multisig.ReceiveSpendRequest(context)
	assert.NoError(err, "failed receiving spend request")

	// inspect the request
	assert.NotNil(request.Token, "request doesn't contain a token")

	// receive the assembled tx (without endorsing) so we can inspect it
	tx, err := multisig.ReceiveSpendTx(context, request)
	assert.NoError(err, "failed receiving spend transaction")

	// Business-logic checks live here. The library deliberately does not
	// enforce a fixed policy; each application decides what its co-owners
	// will sign. Two checks that make sense for most deployments:
	//   1. the tx consumes the token named in SpendRequest;
	//   2. the tx does not consume any other token owned by this node
	//      (otherwise the initiator could piggy-back unauthorised inputs).
	inputs, err := tx.Request().Inputs(context.Context())
	assert.NoError(err, "failed reading inputs from received tx")
	assert.True(inputs.Count() > 0, "received tx has no inputs")

	tms, err := token2.GetManagementService(context, token2.WithTMSID(tx.TMSID()))
	assert.NoError(err, "failed getting tms for received tx")
	sigService := tms.SigService()

	matched := false
	for i := range inputs.Count() {
		in := inputs.At(i)
		if in.Id != nil && in.Id.Equal(request.Token.Id) {
			matched = true

			continue
		}
		// Any other input must NOT belong to this responder.
		if len(in.Owner) != 0 && sigService.IsMe(context.Context(), in.Owner) {
			assert.Fail("received tx consumes additional token [%s] owned by this responder", in.Id)
		}
	}
	assert.True(matched, "received tx does not consume the token named in SpendRequest")

	// Checks passed — endorse explicitly.
	_, err = context.RunView(ttx.NewEndorseView(tx.Transaction))
	assert.NoError(err, "failed to endorse spend transaction")

	// Sanity checks:
	// - the transaction is in pending state
	owner := ttx.NewOwner(context, tx.TokenService())
	vc, _, err := owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Pending, vc, "transaction [%s] should be in busy state", tx.ID())

	// Before completing, the recipient waits for finality of the transaction
	_, err = context.RunView(multisig.NewFinalityView(tx))
	assert.NoError(err, "new tokens were not committed")

	// Sanity checks:
	// - the transaction is in confirmed state
	vc, _, err = owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Confirmed, vc, "transaction [%s] should be in valid state", tx.ID())

	// TODO: Check that the tokens are or are not in the db
	// AssertTokens(context, tx.Transaction, outputs, id)

	return nil, nil
}

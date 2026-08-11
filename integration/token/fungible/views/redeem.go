/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Redeem contains the input information for a redeem
type Redeem struct {
	// Auditor is the name of the auditor that must be contacted to approve the operation
	Auditor string
	// Issuer is the name of the issuer that must be contacted to approve the operation
	Issuer string
	// IssuerPublicParamsPublicKey is the public key of the issuer against which the issuer signature in the Redeem action will be checked.
	IssuerPublicParamsPublicKey view.Identity
	// Wallet is the identifier of the wallet that owns the tokens to redeem
	Wallet string
	// TokenIDs contains a list of token ids to redeem. If empty, tokens are selected on the spot.
	TokenIDs []*token.ID
	// Type of tokens to redeem
	Type token.Type
	// Amount to redeem
	Amount uint64
	// The TMS to pick in case of multiple TMSIDs
	TMSID *token2.TMSID
}

type RedeemView struct {
	*Redeem
}

func (t *RedeemView) Call(context view.Context) (any, error) {
	// The sender directly prepare the token transaction.
	// The sender creates an anonymous transaction (this means that the resulting Fabric transaction will be signed using idemix, for example),
	// and specify the auditor that must be contacted to approve the operation.
	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	tx, err := ttx.NewAnonymousTransaction(
		context,
		TxOpts(t.TMSID, ttx.WithAuditor(idProvider.Identity(t.Auditor)))...,
	)
	assert.NoError(err, "failed creating transaction")

	// The sender will select tokens owned by this wallet
	senderWallet := ttx.GetWallet(context, t.Wallet, ServiceOpts(t.TMSID)...)
	assert.NotNil(senderWallet, "sender wallet [%s] not found", t.Wallet)

	// The sender adds a new redeem operation to the transaction following the instruction received.
	// Notice the use of `token2.WithTokenIDs(t.TokenIDs...)`. If t.TokenIDs is not empty, the Redeem
	// function uses those tokens, otherwise the tokens will be selected on the spot.
	// Token selection happens internally by invoking the default token selector:
	// selector, err := tx.TokenService().SelectorManager().NewSelector(tx.ID())
	// assert.NoError(err, "failed getting selector")
	// selector.Select(wallet, amount, tokenType)
	// It is also possible to pass a custom token selector to the Redeem function by using the relative opt:
	// token2.WithTokenSelector(selector).
	// Notice also the use of `ttx.WithFSCIssuerIdentity(idProvider.Identity(t.Issuer))`.
	// Recall that a redeem operation must be approved by an issuer.
	// Therefore, we need a way to contact the issuer to obtain its signature.
	// If the application doesn't have a way to resolve the Issuer's network node already,
	// the developer can specify the issuer network node identity directly as in this example.
	opts := []token2.TransferOption{token2.WithTokenIDs(t.TokenIDs...)}
	if t.Issuer != "" {
		opts = append(opts, ttx.WithFSCIssuerIdentity(idProvider.Identity(t.Issuer)))
		opts = append(opts, ttx.WithIssuerPublicParamsPublicKey(t.IssuerPublicParamsPublicKey))
	}
	err = tx.Redeem(
		senderWallet,
		t.Type,
		t.Amount,
		opts...)
	assert.NoError(err, "failed adding new tokens")

	// The sender is ready to collect all the required signatures.
	// In this case, the sender's and the auditor's signatures.
	// Invoke the Token Chaincode to collect endorsements on the Token Request and prepare the relative transaction.
	// This is all done in one shot running the following view.
	// Before completing, all recipients receive the approved transaction.
	// Depending on the token driver implementation, the recipient's signature might or might not be needed to make
	// the token transaction valid.
	_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to sign transaction")

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

type RedeemViewFactory struct{}

func (p *RedeemViewFactory) NewView(in []byte) (view.View, error) {
	f := &RedeemView{Redeem: &Redeem{}}
	err := json.Unmarshal(in, f.Redeem)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type IssuerRedeemAcceptView struct{}

func (a *IssuerRedeemAcceptView) Call(context view.Context) (any, error) {
	// Verify Token Request against Metadata
	tx, err := ttx.ReceiveTransaction(context)
	if err != nil {
		return nil, errors.Wrap(err, "failed getting transaction")
	}

	// Sign the transaction
	// EndorserView generates the signature and send in back on the same communication session
	_, err = context.RunView(ttx.NewEndorseView(tx))
	if err != nil {
		return nil, errors.Wrap(err, "issuer failed endorsing transaction")
	}

	return nil, nil
}

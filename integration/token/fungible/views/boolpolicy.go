/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"
	"fmt"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	bptx "github.com/LFDT-Panurus/panurus/token/services/ttx/boolpolicy"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/id"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// PolicyLock contains the input parameters for locking tokens under a policy identity.
type PolicyLock struct {
	// Auditor is the name of the auditor FSC node.
	Auditor string
	// Wallet is the sender's owner wallet identifier.
	Wallet string
	// Type is the token type to transfer.
	Type token.Type
	// Amount is the number of tokens to lock.
	Amount uint64
	// Policy is the boolean policy expression, e.g. "$0 OR $1".
	Policy string
	// PolicyParties are the FSC node identities that become co-owners.
	PolicyParties []view.Identity
	// TMSID optionally pins a specific TMS.
	TMSID *token2.TMSID
	// NotAnonymous disables anonymous transactions when true.
	NotAnonymous bool
}

// PolicyLockView transfers tokens to a policy identity owner.
type PolicyLockView struct {
	*PolicyLock
}

// Call implements view.View.
func (lv *PolicyLockView) Call(context view.Context) (any, error) {
	// Collect a policy identity from all co-owner parties.
	recipient, err := bptx.RequestRecipientIdentity(context, lv.Policy, lv.PolicyParties, token2.WithTMSIDPointer(lv.TMSID))
	assert.NoError(err, "failed requesting policy recipient identity")

	senderWallet := ttx.GetWallet(context, lv.Wallet, token2.WithTMSIDPointer(lv.TMSID))
	assert.NotNil(senderWallet, "sender wallet [%s] not found", lv.Wallet)

	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	tx, err := ttx.NewTransaction(context, nil,
		ttx.WithTMSIDPointer(lv.TMSID),
		ttx.WithAuditor(idProvider.Identity(lv.Auditor)),
		ttx.WithAnonymousTransaction(!lv.NotAnonymous),
	)
	assert.NoError(err, "failed creating transaction")

	assert.NoError(bptx.Wrap(tx).Lock(senderWallet, lv.Type, lv.Amount, recipient), "failed adding lock")

	_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
	assert.NoError(err, "failed to collect endorsements")

	owner := ttx.NewOwner(context, tx.TokenService())
	vc, _, err := owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Pending, vc, "transaction [%s] should be in pending state", tx.ID())

	_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed asking ordering")

	vc, _, err = owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Confirmed, vc, "transaction [%s] should be in confirmed state", tx.ID())

	return tx.ID(), nil
}

// PolicyLockViewFactory creates PolicyLockView instances from serialised parameters.
type PolicyLockViewFactory struct{}

// NewView implements view.Factory.
func (f *PolicyLockViewFactory) NewView(in []byte) (view.View, error) {
	v := &PolicyLockView{PolicyLock: &PolicyLock{}}
	assert.NoError(json.Unmarshal(in, v.PolicyLock), "failed unmarshalling input")

	return v, nil
}

// ---------------------------------------------------------------------------
// Unified policy spend.
// ---------------------------------------------------------------------------

// PolicySpend contains the parameters for spending a policy token.
//
// When Signers is non-nil it restricts signature collection to those
// component identities (OR-policy optimisation: only the listed parties sign,
// leaving other slots nil so the policy evaluator treats them as absent).
// When Signers is nil or empty, all co-owners are notified via
// RequestSpendView and must sign (AND-policy behaviour).
type PolicySpend struct {
	// Auditor is the name of the auditor FSC node.
	Auditor string
	// Wallet is the spender's owner wallet identifier.
	Wallet string
	// Recipient is the FSC node identity that will receive the tokens.
	Recipient view.Identity
	// TMSID optionally pins a specific TMS.
	TMSID *token2.TMSID
	// TokenType is the type of tokens to spend.
	TokenType token.Type
	// Signers, when non-nil, restricts which component identities are asked to
	// sign.  Leave nil to require all co-owners (AND behaviour).
	Signers []view.Identity
}

// PolicySpendView spends a policy token.  OR-policy callers supply Signers to
// skip co-owner notification and restrict signing to just those identities;
// AND-policy callers leave Signers nil so every co-owner is contacted first.
type PolicySpendView struct {
	*PolicySpend
}

// Call implements view.View.
func (r *PolicySpendView) Call(context view.Context) (any, error) {
	serviceOpts := ServiceOpts(r.TMSID)
	recipient, err := ttx.RequestRecipientIdentity(context, r.Recipient, serviceOpts...)
	assert.NoError(err, "failed getting recipient")

	spendWallet := bptx.GetWallet(context, r.Wallet, serviceOpts...)
	assert.NotNil(spendWallet, "wallet [%s] not found", r.Wallet)

	pWallet := bptx.Wallet(context, spendWallet)
	assert.NotNil(pWallet, "policy wallet wrapper for [%s] not found", r.Wallet)

	matched, err := pWallet.ListTokens(context.Context(), token2.WithType(r.TokenType))
	assert.NoError(err, "failed to list policy tokens")
	assert.True(matched.Count() == 1, "expected exactly one policy token, got [%d]", matched.Count())

	if len(r.Signers) == 0 {
		// AND behaviour: notify all co-owners before assembling the transaction.
		_, err = context.RunView(bptx.NewRequestSpendView(matched.At(0), serviceOpts...))
		assert.NoError(err, "failed to request policy spend")
	}

	idProvider, err := id.GetProvider(context)
	assert.NoError(err, "failed getting id provider")
	tx, err := ttx.NewAnonymousTransaction(
		context,
		TxOpts(r.TMSID, ttx.WithAuditor(idProvider.Identity(r.Auditor)))...,
	)
	assert.NoError(err, "failed to create policy transaction")
	assert.NoError(bptx.Wrap(tx).Spend(spendWallet, matched.At(0), recipient), "failed adding spend")

	var collectOpts []token2.ServiceOption
	if len(r.Signers) > 0 {
		collectOpts = append(collectOpts, ttx.WithPolicySigners(r.Signers...))
	}
	_, err = context.RunView(ttx.NewCollectEndorsementsView(tx, collectOpts...))
	assert.NoError(err, "failed to collect endorsements on policy spend")

	_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
	assert.NoError(err, "failed to commit policy spend")

	return tx.ID(), nil
}

// PolicySpendViewFactory creates PolicySpendView instances.
type PolicySpendViewFactory struct{}

// NewView implements view.Factory.
func (f *PolicySpendViewFactory) NewView(in []byte) (view.View, error) {
	v := &PolicySpendView{PolicySpend: &PolicySpend{}}
	assert.NoError(json.Unmarshal(in, v.PolicySpend), "failed unmarshalling input")

	return v, nil
}

// PolicyAcceptSpendView is the co-owner responder for AND-policy spend requests.
// It mirrors MultiSigAcceptSpendView.
type PolicyAcceptSpendView struct{}

// Call implements view.View.
func (m *PolicyAcceptSpendView) Call(context view.Context) (any, error) {
	request, err := bptx.ReceiveSpendRequest(context)
	assert.NoError(err, "failed receiving policy spend request")
	assert.NotNil(request.Token, "request doesn't contain a token")

	// Receive the assembled tx (without endorsing) so the co-owner can
	// inspect it; the library no longer hardcodes a check policy.
	tx, err := bptx.ReceiveSpendTx(context, request)
	assert.NoError(err, "failed receiving policy spend transaction")

	// Business-logic checks (the library does not enforce these):
	//   1. the tx consumes the token named in SpendRequest;
	//   2. the tx does not consume any other token owned by this node.
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
		if len(in.Owner) != 0 && sigService.IsMe(context.Context(), in.Owner) {
			assert.Fail("received tx consumes additional token [%s] owned by this responder", in.Id)
		}
	}
	assert.True(matched, "received tx does not consume the token named in SpendRequest")

	_, err = context.RunView(ttx.NewEndorseView(tx.Transaction))
	assert.NoError(err, "failed to endorse policy spend transaction")

	owner := ttx.NewOwner(context, tx.TokenService())
	vc, _, err := owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Pending, vc, "transaction [%s] should be in pending state", tx.ID())

	_, err = context.RunView(bptx.NewFinalityView(tx))
	assert.NoError(err, "new tokens were not committed")

	vc, _, err = owner.GetStatus(context.Context(), tx.ID())
	assert.NoError(err, "failed to retrieve status for transaction [%s]", tx.ID())
	assert.Equal(ttx.Confirmed, vc, "transaction [%s] should be in confirmed state", tx.ID())

	return nil, nil
}

// ---------------------------------------------------------------------------
// Policy-owned balance: lists tokens held under a policy identity.
// ---------------------------------------------------------------------------

// PolicyOwnedBalanceQuery is the input for PolicyOwnedBalanceView.
type PolicyOwnedBalanceQuery struct {
	TMSID  *token2.TMSID
	Wallet string
	Type   token.Type
}

// PolicyOwnedBalanceView returns the total quantity of policy-identity tokens
// visible to the given wallet, following the same pattern as CoOwnedBalanceView.
type PolicyOwnedBalanceView struct {
	*PolicyOwnedBalanceQuery
}

// Call implements view.View.
func (b *PolicyOwnedBalanceView) Call(context view.Context) (any, error) {
	tms, err := token2.GetManagementService(context, ServiceOpts(b.TMSID)...)
	if err != nil {
		return nil, fmt.Errorf("failed getting management service: %w", err)
	}
	wallet, err := tms.WalletManager().OwnerWallet(context.Context(), b.Wallet)
	if err != nil {
		return nil, fmt.Errorf("wallet %s not found: %w", b.Wallet, err)
	}

	precision := tms.PublicParametersManager().PublicParameters().Precision()
	pWallet := bptx.Wallet(context, wallet)
	assert.NotNil(pWallet, "policy wallet wrapper for [%s] not found", b.Wallet)
	policyTokens, err := pWallet.ListTokensIterator(context.Context(), token2.WithType(b.Type))
	assert.NoError(err, "failed to list policy-owned tokens")
	total, err := iterators.Reduce(policyTokens, token.ToQuantitySum(precision))
	assert.NoError(err, "failed to compute sum of policy-owned tokens")

	return Balance{
		Quantity: total.Decimal(),
		Type:     b.Type,
	}, nil
}

// PolicyOwnedBalanceViewFactory creates PolicyOwnedBalanceView instances.
type PolicyOwnedBalanceViewFactory struct{}

// NewView implements view.Factory.
func (f *PolicyOwnedBalanceViewFactory) NewView(in []byte) (view.View, error) {
	v := &PolicyOwnedBalanceView{PolicyOwnedBalanceQuery: &PolicyOwnedBalanceQuery{}}
	if err := json.Unmarshal(in, v.PolicyOwnedBalanceQuery); err != nil {
		return nil, err
	}

	return v, nil
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections/iterators"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type Balance struct {
	TMSID  token.TMSID
	Wallet string
	Type   token2.Type
}

type BalanceResult struct {
	Type     token2.Type
	Quantity string
	Locked   string
	Expired  string
}

// BalanceView is a view used to return:
// 1. The amount of unspent tokens;
// 2. The amount of htlc-locked tokens not yet expired;
// 3. The amount of expired htlc-locked tokens that have been not reclaimed
// for the given wallet.
type BalanceView struct {
	*Balance
}

func (b *BalanceView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(b.TMSID))
	assert.NoError(err, "failed getting management service")
	wallet, err := tms.WalletManager().OwnerWallet(context.Context(), b.Wallet)
	assert.NoError(err, "failed to get wallet [%s]", b.Wallet)
	precision := tms.PublicParametersManager().PublicParameters().Precision()

	// owned
	balance, err := wallet.Balance(context.Context(), token.WithType(b.Type))
	assert.NoError(err, "failed to get unspent tokens")

	htlcWallet := htlc.Wallet(context, wallet)
	// locked
	lockedToTokens, err := htlcWallet.ListTokensIterator(context.Context(), token.WithType(b.Type))
	assert.NoError(err, "failed to get locked tokens")
	lockedSum, err := iterators.Reduce(lockedToTokens, token2.ToQuantitySum(precision))
	assert.NoError(err, "failed to compute the sum of the htlc locked tokens")

	// expired
	err = htlcWallet.DeleteExpiredReceivedTokens(context, token.WithType(b.Type))
	assert.NoError(err, "failed to delete expired tokens")
	expiredTokens, err := htlcWallet.ListExpiredReceivedTokensIterator(context.Context(), token.WithType(b.Type))
	assert.NoError(err, "failed to get expired tokens")
	expiredSum, err := iterators.Reduce(expiredTokens, token2.ToQuantitySum(precision))
	assert.NoError(err, "failed to compute the sum of the htlc expired tokens")

	return BalanceResult{
		Quantity: balance.String(),
		Locked:   lockedSum.Decimal(),
		Expired:  expiredSum.Decimal(),
		Type:     b.Type,
	}, nil
}

type BalanceViewFactory struct{}

func (g *BalanceViewFactory) NewView(in []byte) (view.View, error) {
	f := &BalanceView{Balance: &Balance{}}
	if err := json.Unmarshal(in, f.Balance); err != nil {
		return nil, err
	}

	return f, nil
}

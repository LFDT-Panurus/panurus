/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// ListUnspentTokens contains the input to query the list of unspent tokens
type ListUnspentTokens struct {
	// Wallet whose identities own the token
	Wallet string
	// TokenType is the token type to select
	TokenType token2.Type
	// The TMS to pick in case of multiple TMSIDs
	TMSID *token.TMSID
}

type ListUnspentTokensView struct {
	*ListUnspentTokens
}

func (p *ListUnspentTokensView) Call(context view.Context) (any, error) {
	// Tokens owner by identities in this wallet will be listed
	wallet := ttx.GetWallet(context, p.Wallet, ServiceOpts(p.TMSID)...)
	assert.NotNil(wallet, "wallet [%s] not found", p.Wallet)

	// Return the list of unspent tokens by type
	return wallet.ListUnspentTokens(context.Context(), ttx.WithType(p.TokenType))
}

type ListUnspentTokensViewFactory struct{}

func (i *ListUnspentTokensViewFactory) NewView(in []byte) (view.View, error) {
	f := &ListUnspentTokensView{ListUnspentTokens: &ListUnspentTokens{}}
	err := json.Unmarshal(in, f.ListUnspentTokens)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type ListOwnerWalletIDsView struct{}

func (p *ListOwnerWalletIDsView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context)
	assert.NoError(err)

	return tms.WalletManager().OwnerWalletIDs(context.Context())
}

type ListOwnerWalletIDsViewFactory struct{}

func (i *ListOwnerWalletIDsViewFactory) NewView(in []byte) (view.View, error) {
	f := &ListOwnerWalletIDsView{}

	return f, nil
}

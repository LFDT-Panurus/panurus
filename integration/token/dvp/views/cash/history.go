/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cash

import (
	"encoding/json"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	"github.com/LFDT-Panurus/panurus/token/services/ttx"
)

// ListIssuedTokens contains the input to query the list of issued tokens
type ListIssuedTokens struct {
	// Wallet whose identities own the token
	Wallet string
	// TokenType is the token type to select
	TokenType token2.Type
}

type ListIssuedTokensView struct {
	*ListIssuedTokens
}

func (p *ListIssuedTokensView) Call(context view.Context) (any, error) {
	// Tokens issued by identities in this wallet will be listed
	wallet := ttx.GetIssuerWallet(context, p.Wallet)
	assert.NotNil(wallet, "wallet [%s] not found", p.Wallet)

	// Return the list of issued tokens by type
	return wallet.ListIssuedTokens(context.Context(), ttx.WithType(p.TokenType))
}

type ListIssuedTokensViewFactory struct{}

func (i *ListIssuedTokensViewFactory) NewView(in []byte) (view.View, error) {
	f := &ListIssuedTokensView{ListIssuedTokens: &ListIssuedTokens{}}
	err := json.Unmarshal(in, f.ListIssuedTokens)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

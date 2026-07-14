/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"encoding/json"
	"fmt"

	"github.com/LFDT-Panurus/panurus/token"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type BalanceQuery struct {
	TMSID  token.TMSID
	Wallet string
	Type   token2.Type
}

type Balance struct {
	Type     token2.Type
	Quantity string
}

type BalanceView struct {
	*BalanceQuery
}

func (b *BalanceView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(b.TMSID))
	assert.NoError(err)
	wallet, err := tms.WalletManager().OwnerWallet(context.Context(), b.Wallet)
	if err != nil {
		return nil, fmt.Errorf("wallet %s not found: %w", b.Wallet, err)
	}

	balance, err := wallet.Balance(context.Context(), token.WithType(b.Type))
	if err != nil {
		return nil, err
	}

	return Balance{Quantity: balance.String(), Type: b.Type}, nil
}

type BalanceViewFactory struct{}

func (g *BalanceViewFactory) NewView(in []byte) (view.View, error) {
	f := &BalanceView{BalanceQuery: &BalanceQuery{}}
	if err := json.Unmarshal(in, f.BalanceQuery); err != nil {
		return nil, err
	}

	return f, nil
}

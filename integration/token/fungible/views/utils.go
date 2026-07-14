/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package views

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/kvs"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// AssertTokens checks that the tokens are or are not in the tokendb
func AssertTokens(sp token.ServiceProvider, tx *ttx.Transaction, outputs *token.OutputStream, id view.Identity) {
	ctx := context.Background()
	db, err := tokendb.GetByTMSId(sp, tx.TokenService().ID())
	assert.NoError(err, "failed to get token db for [%s]", tx.TokenService().ID())
	for _, output := range outputs.Outputs() {
		tokenID := output.ID(token.RequestAnchor(tx.ID()))
		if output.Owner.Equal(id) || tx.TokenService().SigService().IsMe(ctx, output.Owner) {
			// check it exists
			toks, err := db.GetTokens(ctx, tokenID)
			assert.NoError(err, "failed to retrieve token [%s]", tokenID)
			assert.Equal(1, len(toks), "expected one token")
			assert.Equal(output.Quantity.Hex(), toks[0].Quantity, "token quantity mismatch")
			assert.Equal(output.Type, toks[0].Type, "token type mismatch")
		} else {
			// check it does not exist
			_, err := db.GetTokens(ctx, tokenID)
			assert.Error(err, "token [%s] should not exist", tokenID)
			assert.True(strings.Contains(err.Error(), "token not found"))
		}
	}
}

// ServiceOpts creates a new array of token.ServiceOption containing token.WithTMSID and any additional token.ServiceOption passed to this function
func ServiceOpts(tmsId *token.TMSID, opts ...token.ServiceOption) []token.ServiceOption {
	var serviceOpts []token.ServiceOption
	if tmsId != nil {
		serviceOpts = append(serviceOpts, token.WithTMSID(*tmsId))
	}

	return append(serviceOpts, opts...)
}

func TxOpts(tmsId *token.TMSID, opts ...ttx.TxOption) []ttx.TxOption {
	var txOpts []ttx.TxOption
	if tmsId != nil {
		txOpts = append(txOpts, ttx.WithTMSID(*tmsId))
	}
	txOpts = append(txOpts, opts...)

	return txOpts
}

func GetKVS(sp services.Provider) *kvs.KVS {
	kvss, err := sp.GetService(&kvs.KVS{})
	if err != nil {
		panic(err)
	}

	return kvss.(*kvs.KVS)
}

type KVSEntry struct {
	Key   string
	Value string
}

type SetKVSEntryView struct {
	*KVSEntry
}

func (s *SetKVSEntryView) Call(context view.Context) (any, error) {
	assert.NoError(GetKVS(context).Put(context.Context(), s.Key, s.Value), "failed to put in KVS [%s:%s]", s.Key, s.Value)

	return nil, nil
}

type SetKVSEntryViewFactory struct{}

func (p *SetKVSEntryViewFactory) NewView(in []byte) (view.View, error) {
	f := &SetKVSEntryView{KVSEntry: &KVSEntry{}}
	err := json.Unmarshal(in, f.KVSEntry)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

type SetSpendableFlag struct {
	TMSID     token.TMSID
	TokenID   token2.ID
	Spendable bool
}

type SetSpendableFlagView struct {
	*SetSpendableFlag
}

func (s *SetSpendableFlagView) Call(context view.Context) (any, error) {
	tms, err := token.GetManagementService(context, token.WithTMSID(s.TMSID))
	assert.NoError(err, "failed getting management service")
	assert.NotNil(tms, "failed getting token management service [%s]", s.TMSID)

	tokens, err := tokens.GetService(context, tms.ID())
	assert.NoError(err, "failed getting tokens")

	err = tokens.SetSpendableFlag(context.Context(), s.Spendable, &s.TokenID)
	assert.NoError(err, "failed setting spendable flag")

	return nil, nil
}

type SetSpendableFlagViewFactory struct{}

func (p *SetSpendableFlagViewFactory) NewView(in []byte) (view.View, error) {
	f := &SetSpendableFlagView{SetSpendableFlag: &SetSpendableFlag{}}
	err := json.Unmarshal(in, f.SetSpendableFlag)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

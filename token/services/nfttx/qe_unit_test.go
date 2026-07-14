/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type House struct {
	LinearID  string
	Address   string
	Valuation uint64
}

func TestNewQueryExecutor(t *testing.T) {
	assert.Panics(t, func() {
		_, _ = nfttx.NewQueryExecutor(nil, "my-wallet", 64)
	})
	qe, err := nfttx.NewQueryExecutor(getErrorCtx(), "my-wallet", 64)
	require.Error(t, err)
	assert.Nil(t, qe)
}

func TestQueryExecutor_QueryByKey_FilterErrors(t *testing.T) {
	fakeSelector := &mock.Selector{}
	fakeVault := &mock.Vault{}

	qe := nfttx.NewTestQueryExecutor(fakeSelector, fakeVault, 64)

	fakeSelector.FilterReturns(nil, nfttx.ErrNoResults)
	err := qe.QueryByKey(context.TODO(), &House{}, "LinearID", "123")
	require.ErrorIs(t, err, nfttx.ErrNoResults)

	fakeSelector.FilterReturns(nil, errors.New("some filter error"))
	err = qe.QueryByKey(context.TODO(), &House{}, "LinearID", "123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some filter error")
}

func TestQueryExecutor_QueryByKey_VaultErrors(t *testing.T) {
	fakeSelector := &mock.Selector{}
	fakeVault := &mock.Vault{}

	qe := nfttx.NewTestQueryExecutor(fakeSelector, fakeVault, 64)

	fakeSelector.FilterReturns([]*token2.ID{{TxId: "tx1", Index: 0}}, nil)
	fakeVault.GetTokensReturns(nil, errors.New("vault error"))
	err := qe.QueryByKey(context.TODO(), &House{}, "LinearID", "123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault error")
}

func TestQueryExecutor_QueryByKey_Success(t *testing.T) {
	fakeSelector := &mock.Selector{}
	fakeVault := &mock.Vault{}

	qe := nfttx.NewTestQueryExecutor(fakeSelector, fakeVault, 64)

	h := &House{
		LinearID:  "123",
		Address:   "5th Avenue",
		Valuation: 100,
	}
	raw, err := json.Marshal(h)
	require.NoError(t, err)

	tokens := []*token2.Token{
		{
			Type:     token2.Type(base64.StdEncoding.EncodeToString(raw)),
			Quantity: "0x01",
		},
	}
	fakeSelector.FilterReturns([]*token2.ID{{TxId: "tx1", Index: 0}}, nil)
	fakeVault.GetTokensReturns(tokens, nil)

	var house House
	err = qe.QueryByKey(context.TODO(), &house, "LinearID", "123")
	require.NoError(t, err)
	assert.Equal(t, "123", house.LinearID)

	// fail decoding type
	tokens[0].Type = "not-base64"
	err = qe.QueryByKey(context.TODO(), &house, "LinearID", "123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode type")

	// test no matching token, empty tokens list
	fakeVault.GetTokensReturns([]*token2.Token{}, nil)
	err = qe.QueryByKey(context.TODO(), &house, "LinearID", "123")
	require.ErrorIs(t, err, nfttx.ErrNoResults)
}

func TestQueryExecutor_QueryByKey_BadQuantity(t *testing.T) {
	fakeSelector := &mock.Selector{}
	fakeVault := &mock.Vault{}

	qe := nfttx.NewTestQueryExecutor(fakeSelector, fakeVault, 64)

	tokens := []*token2.Token{
		{Type: "abc", Quantity: "bad-quantity"},
	}
	fakeSelector.FilterReturns([]*token2.ID{{TxId: "tx1", Index: 0}}, nil)
	fakeVault.GetTokensReturns(tokens, nil)

	err := qe.QueryByKey(context.TODO(), &House{}, "LinearID", "123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to convert quantity")
}

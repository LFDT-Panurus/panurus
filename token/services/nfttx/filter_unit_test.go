/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyIterator struct {
	tokens []*token2.UnspentToken
	index  int
	err    error
}

func (d *dummyIterator) Next() (*token2.UnspentToken, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.index >= len(d.tokens) {
		return nil, nil
	}
	t := d.tokens[d.index]
	d.index++

	return t, nil
}

func (d *dummyIterator) Close() {}

type testFilter struct {
	pass bool
}

func (f *testFilter) ContainsToken(token *token2.UnspentToken) bool {
	return f.pass
}

func TestNewFilter(t *testing.T) {
	fakeQS := &mock.QueryService{}
	f := nfttx.NewFilter("my-wallet", fakeQS, 64)
	assert.NotNil(t, f)
}

func TestFilterNilFilter(t *testing.T) {
	f := nfttx.NewFilter("my-wallet", nil, 64)
	ids, err := f.Filter(context.Background(), nil, "1")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.Contains(t, err.Error(), "filter is nil")
}

func TestFilterIteratorError(t *testing.T) {
	fakeQS := &mock.QueryService{}
	fakeQS.UnspentTokensIteratorByReturns(nil, errors.New("iterator error"))

	f := nfttx.NewFilter("my-wallet", fakeQS, 64)
	ids, err := f.Filter(context.Background(), &testFilter{pass: true}, "1")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.Contains(t, err.Error(), "token selection failed")
}

func TestFilterNextError(t *testing.T) {
	fakeQS := &mock.QueryService{}
	fakeQS.UnspentTokensIteratorByReturns(&dummyIterator{err: errors.New("next error"), tokens: nil}, nil)

	f := nfttx.NewFilter("my-wallet", fakeQS, 64)
	ids, err := f.Filter(context.Background(), &testFilter{pass: true}, "1")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.Contains(t, err.Error(), "token selection failed")
}

func TestFilterBadQuantityTarget(t *testing.T) {
	f := nfttx.NewFilter("my-wallet", nil, 64)
	ids, err := f.Filter(context.Background(), &testFilter{pass: true}, "xyz")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.Contains(t, err.Error(), "failed to select tokens: failed to convert quantity")
}

func TestFilterSuccess(t *testing.T) {
	fakeQS := &mock.QueryService{}
	tokens := []*token2.UnspentToken{
		{Id: token2.ID{TxId: "tx1", Index: 0}, Quantity: "1"},
		{Id: token2.ID{TxId: "tx2", Index: 0}, Quantity: "1"},
	}
	fakeQS.UnspentTokensIteratorByReturns(&dummyIterator{tokens: tokens}, nil)

	f := nfttx.NewFilter("my-wallet", fakeQS, 64)

	ids, err := f.Filter(context.Background(), &testFilter{pass: true}, "2")
	require.NoError(t, err)
	assert.Len(t, ids, 2)

	// Filter returning false should be ignored
	fakeQS.UnspentTokensIteratorByReturns(&dummyIterator{tokens: tokens}, nil)
	ids, err = f.Filter(context.Background(), &testFilter{pass: false}, "1")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.ErrorIs(t, err, nfttx.ErrNoResults) // Note here we use unwrapping since err could be "failed to select tokens: no results found"
}

func TestFilterInsufficientTokens(t *testing.T) {
	fakeQS := &mock.QueryService{}
	tokens := []*token2.UnspentToken{
		{Id: token2.ID{TxId: "tx1", Index: 0}, Quantity: "1"},
	}
	fakeQS.UnspentTokensIteratorByReturns(&dummyIterator{tokens: tokens}, nil)

	f := nfttx.NewFilter("my-wallet", fakeQS, 64)
	ids, err := f.Filter(context.Background(), &testFilter{pass: true}, "2")
	require.Error(t, err)
	assert.Nil(t, ids)
	assert.ErrorIs(t, err, nfttx.ErrNoResults)
}

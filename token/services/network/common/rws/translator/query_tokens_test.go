/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package translator_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newQueryTokensTranslator() (*translator.Translator, *mock.RWSet) {
	rws := &mock.RWSet{}
	rws.GetStateReturns([]byte("value"), nil)

	return translator.New("0", translator.NewRWSetWrapper(rws, tokenNameSpace, "0"), &keys.Translator{}), rws
}

// The ids reach QueryTokens straight from a JSON array decoded on the chaincode's query path, where
// a `null` element decodes to a nil *token.ID. A nil entry must be reported as an invalid request
// rather than dereferenced, and must not stop the ledger read of the valid ids around it.
func TestQueryTokens_NilIDIsRejectedNotDereferenced(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []*token.ID
	}{
		{"only a nil id", []*token.ID{nil}},
		{"nil id first", []*token.ID{nil, {TxId: "tx", Index: 0}}},
		{"nil id last", []*token.ID{{TxId: "tx", Index: 0}, nil}},
		{"all nil ids", []*token.ID{nil, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := newQueryTokensTranslator()

			var res [][]byte
			var err error
			require.NotPanics(t, func() { res, err = w.QueryTokens(context.Background(), tc.ids) })
			require.Error(t, err)
			require.ErrorContains(t, err, "nil token id at index")
			assert.Nil(t, res)
		})
	}
}

func TestQueryTokens_ValidIDsAreRead(t *testing.T) {
	w, rws := newQueryTokensTranslator()

	res, err := w.QueryTokens(context.Background(), []*token.ID{{TxId: "tx", Index: 0}, {TxId: "tx", Index: 1}})
	require.NoError(t, err)
	assert.Len(t, res, 2)
	assert.Equal(t, 2, rws.GetStateCallCount())
}

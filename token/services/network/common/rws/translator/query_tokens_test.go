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
	"github.com/stretchr/testify/require"
)

// TestQueryTokensNilID makes sure a nil id is reported as an error instead of being
// dereferenced. See https://github.com/LFDT-Panurus/panurus/issues/2051.
func TestQueryTokensNilID(t *testing.T) {
	fakeRWSet := &mock.RWSet{}
	fakeRWSet.GetStateReturns([]byte("token"), nil)
	w := translator.New("0", translator.NewRWSetWrapper(fakeRWSet, tokenNameSpace, "0"), &keys.Translator{})

	for _, ids := range [][]*token.ID{
		{nil},
		{nil, nil},
		{{TxId: "tx1", Index: 0}, nil},
	} {
		require.NotPanics(t, func() {
			res, err := w.QueryTokens(context.Background(), ids)
			require.Error(t, err)
			require.Contains(t, err.Error(), "nil token id at position")
			require.Nil(t, res)
		})
	}
}

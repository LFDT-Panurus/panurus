/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"testing"

	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireAllPresent_AllPresent(t *testing.T) {
	ids := []*token2.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 0}}
	tokens := [][]byte{[]byte("content1"), []byte("content2")}

	require.NoError(t, requireAllPresent(ids, tokens))
}

// QueryTokens reports an absent token as a nil entry at its position, not an
// error, so GetTokenView must reject it itself rather than let a caller that
// forgot to check nils believe every requested token exists.
func TestRequireAllPresent_NilEntryRejected(t *testing.T) {
	ids := []*token2.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 0}}
	tokens := [][]byte{[]byte("content1"), nil}

	err := requireAllPresent(ids, tokens)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tx2")
	assert.Contains(t, err.Error(), "does not exist on the ledger")
}

func TestRequireAllPresent_EmptyEntryRejected(t *testing.T) {
	ids := []*token2.ID{{TxId: "tx1", Index: 0}}
	tokens := [][]byte{{}}

	require.Error(t, requireAllPresent(ids, tokens))
}

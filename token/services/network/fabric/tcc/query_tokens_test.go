/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc_test

import (
	"encoding/json"
	"fmt"
	"testing"

	chaincode2 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

// newQueryStub returns a stub usable by the query entry points, which need neither public
// parameters nor a validator.
func newQueryStub() *mock.ChaincodeStubInterface {
	stub := &mock.ChaincodeStubInterface{}
	stub.GetTxIDReturns("txid")

	return stub
}

// TestQueryTokensNullID covers https://github.com/LFDT-Panurus/panurus/issues/2051: a null
// element in the ids array decodes to a nil *token.ID and used to be dereferenced by the
// translator.
func TestQueryTokensNullID(t *testing.T) {
	cc := &chaincode2.TokenChaincode{}

	require.NotPanics(t, func() {
		resp := cc.QueryTokens([]byte(`[null]`), newQueryStub())
		require.Equal(t, int32(500), resp.Status)
		require.Contains(t, resp.Message, "invalid token id at position [0]: null")
	})
}

func TestQueryTokensNullIDAmongValidOnes(t *testing.T) {
	cc := &chaincode2.TokenChaincode{}

	require.NotPanics(t, func() {
		resp := cc.QueryTokens([]byte(`[{"tx_id":"tx1","index":0},null]`), newQueryStub())
		require.Equal(t, int32(500), resp.Status)
		require.Contains(t, resp.Message, "invalid token id at position [1]: null")
	})
}

// TestQueryTokensEmptyID covers the neighbouring case: an id that is present but carries no
// tx id. It never panicked, but it used to reach the ledger and come back as a missing
// composite key; it is now rejected before any state read.
func TestQueryTokensEmptyID(t *testing.T) {
	cc := &chaincode2.TokenChaincode{}

	for position, raw := range map[int]string{
		0: `[{}]`,
		1: `[{"tx_id":"tx1","index":0},{"tx_id":"","index":3}]`,
	} {
		stub := newQueryStub()
		resp := cc.QueryTokens([]byte(raw), stub)
		require.Equal(t, int32(500), resp.Status, "input [%s]", raw)
		require.Contains(t, resp.Message, fmt.Sprintf("invalid token id at position [%d]: empty tx id", position))
		require.Zero(t, stub.GetStateCallCount(), "input [%s] must be rejected before any state read", raw)
	}
}

// TestInvokeQueryTokensNullID exercises the same input through the chaincode dispatcher, to
// make sure the response is a plain validation error and not a recovered panic.
func TestInvokeQueryTokensNullID(t *testing.T) {
	cc := &chaincode2.TokenChaincode{}
	stub := newQueryStub()
	stub.GetArgsReturns([][]byte{[]byte(chaincode2.QueryTokensFunctions), []byte(`[null]`)})

	resp := cc.Invoke(stub)
	require.Equal(t, int32(500), resp.Status)
	require.Contains(t, resp.Message, "invalid token id at position [0]: null")
	require.NotContains(t, resp.Message, "failed responding")
}

func TestQueryTokensMalformedJSON(t *testing.T) {
	cc := &chaincode2.TokenChaincode{}

	for _, raw := range []string{``, `[`, `{}`, `[{"tx_id":`, `["not-an-id"]`} {
		require.NotPanics(t, func() {
			resp := cc.QueryTokens([]byte(raw), newQueryStub())
			require.Equal(t, int32(500), resp.Status, "input [%s]", raw)
		})
	}
}

// FuzzQueryTokensNoPanic fuzzes the raw, caller-controlled argument of the queryTokens
// chaincode function. QueryTokens must always answer with a response, never panic.
func FuzzQueryTokensNoPanic(f *testing.F) {
	valid, err := json.Marshal([]*token2.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}})
	require.NoError(f, err)

	f.Add(valid)
	f.Add([]byte(``))
	f.Add([]byte(`[]`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[null]`))
	f.Add([]byte(`[null,null]`))
	f.Add([]byte(`[{"tx_id":"tx1","index":0},null]`))
	f.Add([]byte(`[{"tx_id":"tx1","index":0}`))
	f.Add([]byte(`[{"tx_id":"tx1","index":-1}]`))
	f.Add([]byte(`[{"tx_id":"tx1","index":18446744073709551616}]`))
	f.Add([]byte(`{"tx_id":"tx1","index":0}`))
	f.Add([]byte(`["tx1"]`))
	f.Add([]byte(`[{}]`))
	f.Add([]byte(`[{"tx_id":""}]`))
	f.Add([]byte("[{\"tx_id\":\"a\u0000b\"}]"))

	f.Fuzz(func(t *testing.T, idsRaw []byte) {
		cc := &chaincode2.TokenChaincode{}
		resp := cc.QueryTokens(idsRaw, newQueryStub())
		require.NotNil(t, resp)
	})
}

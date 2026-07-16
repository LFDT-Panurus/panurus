/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package qe_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/qe"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabricx/qe/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testNetwork   = "test-network"
	testChannel   = "test-channel"
	testNamespace = "test-namespace"
)

// outputKey returns the output key for the given token ID, computed the same
// way the Executor does internally, so tests can assert on the keys passed
// to the query service without duplicating key-encoding logic.
func outputKey(t *testing.T, txID string, index uint64) driver.PKey {
	t.Helper()
	k, err := (&keys.Translator{}).CreateOutputKey(txID, index)
	require.NoError(t, err)

	return k
}

// newExecutor wires an Executor to the given mock query service, mirroring
// how NewExecutor and NewExecutorProvider construct one in production.
func newExecutor(qsp *mock.QueryServiceProvider) *qe.Executor {
	return qe.NewExecutor(testNetwork, testChannel, qsp)
}

func TestQueryTokens_AllPresent(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}}
	k1 := outputKey(t, "tx1", 0)
	k2 := outputKey(t, "tx2", 1)

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {
			k1: {Raw: []byte("token1")},
			k2: {Raw: []byte("token2")},
		},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, ids)
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("token1"), []byte("token2")}, res)
}

func TestQueryTokens_SomeMissing_NoError(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}, {TxId: "tx3", Index: 2}}
	k1 := outputKey(t, "tx1", 0)
	k3 := outputKey(t, "tx3", 2)

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	// tx2's key is absent entirely from the result map; tx3's key is present
	// but with an empty Raw value. Both must be treated as "missing", not errors.
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {
			k1: {Raw: []byte("token1")},
			k3: {Raw: nil},
		},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, ids)
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("token1"), nil, nil}, res)
}

func TestQueryTokens_AllMissing_NoError(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}}

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, ids)
	require.NoError(t, err)
	require.Equal(t, [][]byte{nil, nil}, res)
}

func TestQueryTokens_EmptyIDs(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, nil)
	require.NoError(t, err)
	require.Nil(t, res)
	assert.Equal(t, 0, qsp.GetCallCount())
}

func TestQueryTokens_NilIDSkipped(t *testing.T) {
	ids := []*token.ID{nil, {TxId: "tx1", Index: 0}}
	k1 := outputKey(t, "tx1", 0)

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {k1: {Raw: []byte("token1")}},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, ids)
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("token1")}, res)
}

func TestQueryTokens_ProviderError(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}}

	qsp := &mock.QueryServiceProvider{}
	qsp.GetReturns(nil, errors.New("boom"))

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, ids)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "failed getting qs")
	assert.Contains(t, err.Error(), "boom")
}

func TestQueryTokens_GetStatesError(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}}

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(nil, errors.New("rpc failed"))

	e := newExecutor(qsp)
	res, err := e.QueryTokens(t.Context(), testNamespace, ids)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "failed getting states")
	assert.Contains(t, err.Error(), "rpc failed")
}

func TestQueryStates_AllPresent(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {
			"k1": {Raw: []byte("v1")},
			"k2": {Raw: []byte("v2")},
		},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryStates(t.Context(), testNamespace, []string{"k1", "k2"})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("v1"), []byte("v2")}, res)
}

func TestQueryStates_SomeMissing_NoError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {
			"k1": {Raw: []byte("v1")},
			"k3": {Raw: nil}, // present but empty
		},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryStates(t.Context(), testNamespace, []string{"k1", "k2", "k3"})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("v1"), nil, nil}, res)
}

func TestQueryStates_AllMissing_NoError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryStates(t.Context(), testNamespace, []string{"k1", "k2"})
	require.NoError(t, err)
	require.Equal(t, [][]byte{nil, nil}, res)
}

func TestQueryStates_EmptyKeys(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}

	e := newExecutor(qsp)
	res, err := e.QueryStates(t.Context(), testNamespace, nil)
	require.NoError(t, err)
	require.Nil(t, res)
	assert.Equal(t, 0, qsp.GetCallCount())
}

func TestQueryStates_ProviderError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qsp.GetReturns(nil, errors.New("boom"))

	e := newExecutor(qsp)
	res, err := e.QueryStates(t.Context(), testNamespace, []string{"k1"})
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "failed getting qs")
	assert.Contains(t, err.Error(), "boom")
}

func TestQueryStates_GetStatesError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(nil, errors.New("rpc failed"))

	e := newExecutor(qsp)
	res, err := e.QueryStates(t.Context(), testNamespace, []string{"k1"})
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "failed getting states")
	assert.Contains(t, err.Error(), "rpc failed")
}

func TestQueryState_KeyPresent(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {"k1": {Raw: []byte("v1")}},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryState(t.Context(), testNamespace, "k1")
	require.NoError(t, err)
	require.Equal(t, []byte("v1"), res)
}

// TestQueryState_KeyMissing_NoError is a regression guard: before the fix, a
// missing key made QueryStates return an error, which meant a transaction
// with no token-request-hash state (see fabricx/ledger.go GetTransactionStatus)
// would incorrectly fail. Now it must return (nil, nil).
func TestQueryState_KeyMissing_NoError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QueryState(t.Context(), testNamespace, "missing-key")
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestQueryState_UnderlyingError(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}
	qsp.GetReturns(nil, errors.New("boom"))

	e := newExecutor(qsp)
	res, err := e.QueryState(t.Context(), testNamespace, "k1")
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "boom")
}

func TestQuerySpentTokens_MixedPresence(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}}
	k1 := outputKey(t, "tx1", 0)

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	// tx1 is present (unspent) -> false; tx2 is missing (spent) -> true.
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {k1: {Raw: []byte("token1")}},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QuerySpentTokens(t.Context(), testNamespace, ids, nil)
	require.NoError(t, err)
	require.Equal(t, []bool{false, true}, res)
}

// TestQuerySpentTokens_MissingKeyFromResponseMap is a regression guard: the
// result slice must be sized by the number of requested keys, not by the
// number of entries the query service actually returned. Before the fix,
// a key entirely absent from the response map (as opposed to present with
// an empty Raw value) caused an index-out-of-range panic.
func TestQuerySpentTokens_MissingKeyFromResponseMap(t *testing.T) {
	ids := []*token.ID{{TxId: "tx1", Index: 0}, {TxId: "tx2", Index: 1}, {TxId: "tx3", Index: 2}}
	k1 := outputKey(t, "tx1", 0)
	k3 := outputKey(t, "tx3", 2)

	qsp := &mock.QueryServiceProvider{}
	qs := &mock.QueryService{}
	qsp.GetReturns(qs, nil)
	// tx2's key is entirely absent from the response map; the response map
	// itself is also smaller than the number of requested keys.
	qs.GetStatesReturns(map[driver.Namespace]map[driver.PKey]driver.VaultValue{
		testNamespace: {
			k1: {Raw: []byte("token1")},
			k3: {Raw: nil},
		},
	}, nil)

	e := newExecutor(qsp)
	res, err := e.QuerySpentTokens(t.Context(), testNamespace, ids, nil)
	require.NoError(t, err)
	require.Equal(t, []bool{false, true, true}, res)
}

func TestQuerySpentTokens_EmptyIDs(t *testing.T) {
	qsp := &mock.QueryServiceProvider{}

	e := newExecutor(qsp)
	res, err := e.QuerySpentTokens(t.Context(), testNamespace, nil, nil)
	require.NoError(t, err)
	require.Nil(t, res)
	assert.Equal(t, 0, qsp.GetCallCount())
}

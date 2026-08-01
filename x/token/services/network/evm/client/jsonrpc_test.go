/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rpcCall captures one request the client made, so tests can assert the method and params on the
// wire (the JSON-RPC contract with the node), not just the decoded return value.
type rpcCall struct {
	Method string
	Params []any
}

// newTestServer starts a JSON-RPC server whose handler returns, for each method, the raw JSON given
// in results. It records every request for later assertions.
func newTestServer(t *testing.T, results map[string]string) (*JSONRPCClient, *[]rpcCall) {
	t.Helper()
	var calls []rpcCall

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		// assert, not require: this runs on the server goroutine, where FailNow would not stop the test.
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			w.WriteHeader(http.StatusBadRequest)

			return
		}
		calls = append(calls, rpcCall{Method: req.Method, Params: req.Params})

		result, ok := results[req.Method]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))
	}))
	t.Cleanup(srv.Close)

	// #nosec G107 -- httptest local test server URL
	c, err := NewJSONRPCClient(srv.URL, srv.Client())
	require.NoError(t, err)

	return c, &calls
}

func TestNewJSONRPCClientRejectsEmptyEndpoint(t *testing.T) {
	_, err := NewJSONRPCClient("", nil)
	require.Error(t, err)
	_, err = NewJSONRPCClient("   ", nil)
	require.Error(t, err)
}

func TestChainIDAndPing(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{"eth_chainId": `"0x7a69"`})

	got, err := c.ChainID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(31337), got)
	require.NotEmpty(t, *calls)
	assert.Equal(t, "eth_chainId", (*calls)[0].Method)

	require.NoError(t, c.Ping(context.Background()))
}

func TestCallEncodesTargetAndBlockTag(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{"eth_call": `"0x1234"`})

	to, err := HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)
	out, err := c.Call(context.Background(), to, []byte{0xab, 0xcd}, "finalized")
	require.NoError(t, err)
	assert.Equal(t, []byte{0x12, 0x34}, out)

	require.Len(t, *calls, 1)
	assert.Equal(t, "eth_call", (*calls)[0].Method)
	require.Len(t, (*calls)[0].Params, 2)
	arg, ok := (*calls)[0].Params[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, to.Hex(), arg["to"])
	assert.Equal(t, "0xabcd", arg["data"])
	assert.Equal(t, "finalized", (*calls)[0].Params[1], "the block tag must be sent as given")
}

func TestCallDefaultsToFinalized(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{"eth_call": `"0x"`})

	_, err := c.Call(context.Background(), Address{}, nil, "")
	require.NoError(t, err)
	require.Len(t, *calls, 1)
	assert.Equal(t, "finalized", (*calls)[0].Params[1], "an empty tag must default to finalized")
}

func TestCallSurfacesRPCError(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{}) // every method returns a JSON-RPC error

	_, err := c.Call(context.Background(), Address{}, nil, "latest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "method not found")
}

func TestPendingNonceAt(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{"eth_getTransactionCount": `"0x2a"`})

	got, err := c.PendingNonceAt(context.Background(), Address{})
	require.NoError(t, err)
	assert.Equal(t, uint64(42), got)
	require.Len(t, *calls, 1)
	assert.Equal(t, "pending", (*calls)[0].Params[1], "the nonce must include pending transactions")
}

func TestEstimateGas(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{"eth_estimateGas": `"0x5208"`})

	to, err := HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)
	got, err := c.EstimateGas(context.Background(), CallMsg{To: &to, Data: []byte{0x01}})
	require.NoError(t, err)
	assert.Equal(t, uint64(21000), got)
}

// TestSuggestGasFees checks the EIP-1559 fee derivation: the tip comes from the node, and the max
// fee leaves headroom for base-fee growth (baseFee*2 + tip).
func TestSuggestGasFees(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_maxPriorityFeePerGas": `"0x3b9aca00"`,            // 1 gwei
		"eth_getBlockByNumber":     `{"baseFeePerGas":"0x7"}`, // 7 wei
	})

	fees, err := c.SuggestGasFees(context.Background())
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1_000_000_000), fees.MaxPriorityFeePerGas)
	assert.Equal(t, big.NewInt(2*7+1_000_000_000), fees.MaxFeePerGas)
}

// TestSuggestGasFeesPreLondon covers a node whose head block reports no base fee.
func TestSuggestGasFeesPreLondon(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_maxPriorityFeePerGas": `"0x1"`,
		"eth_getBlockByNumber":     `{}`,
	})

	fees, err := c.SuggestGasFees(context.Background())
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1), fees.MaxFeePerGas, "with no base fee the max fee is just the tip")
}

func TestSendRawTransaction(t *testing.T) {
	const txHash = "0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa"
	c, calls := newTestServer(t, map[string]string{"eth_sendRawTransaction": `"` + txHash + `"`})

	got, err := c.SendRawTransaction(context.Background(), []byte{0x02, 0xff})
	require.NoError(t, err)
	assert.Equal(t, txHash, got.Hex())
	require.Len(t, *calls, 1)
	assert.Equal(t, "0x02ff", (*calls)[0].Params[0], "the raw transaction must be sent as hex data")
}

func TestGetTransactionReceiptMined(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_getTransactionReceipt": `{
			"transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa",
			"blockNumber":"0x10",
			"status":"0x1",
			"logs":[{
				"address":"0x5FbDB2315678afecb367f032d93F642f64180aa3",
				"topics":["0x1111111111111111111111111111111111111111111111111111111111111111"],
				"data":"0xbeef",
				"transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa",
				"blockNumber":"0x10"
			}]
		}`,
	})

	got, err := c.GetTransactionReceipt(context.Background(), Hash{})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint64(1), got.Status)
	require.NotNil(t, got.BlockNumber)
	assert.Equal(t, uint64(16), *got.BlockNumber)
	require.Len(t, got.Logs, 1)
	assert.Equal(t, []byte{0xbe, 0xef}, got.Logs[0].Data)
	require.Len(t, got.Logs[0].Topics, 1)
}

// TestGetTransactionReceiptNotMined checks the (nil, nil) contract the finality layer relies on.
func TestGetTransactionReceiptNotMined(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{"eth_getTransactionReceipt": `null`})

	got, err := c.GetTransactionReceipt(context.Background(), Hash{})
	require.NoError(t, err)
	assert.Nil(t, got, "an unmined transaction is (nil, nil), not an error")
}

func TestGetTransactionReceiptReverted(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_getTransactionReceipt": `{
			"transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa",
			"blockNumber":"0x10","status":"0x0","logs":[]
		}`,
	})

	got, err := c.GetTransactionReceipt(context.Background(), Hash{})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint64(0), got.Status, "a reverted transaction reports status 0")
}

// TestIsPending covers the three states the finality resolver distinguishes (design §7.1).
func TestIsPending(t *testing.T) {
	t.Run("pending: known but unmined", func(t *testing.T) {
		c, _ := newTestServer(t, map[string]string{"eth_getTransactionByHash": `{"blockNumber":null}`})
		pending, found, err := c.IsPending(context.Background(), Hash{})
		require.NoError(t, err)
		assert.True(t, found)
		assert.True(t, pending)
	})

	t.Run("mined: has a block number", func(t *testing.T) {
		c, _ := newTestServer(t, map[string]string{"eth_getTransactionByHash": `{"blockNumber":"0x10"}`})
		pending, found, err := c.IsPending(context.Background(), Hash{})
		require.NoError(t, err)
		assert.True(t, found)
		assert.False(t, pending)
	})

	t.Run("dropped: unknown to the node", func(t *testing.T) {
		c, _ := newTestServer(t, map[string]string{"eth_getTransactionByHash": `null`})
		pending, found, err := c.IsPending(context.Background(), Hash{})
		require.NoError(t, err)
		assert.False(t, found, "an unknown transaction must report found=false so it can be treated as dropped")
		assert.False(t, pending)
	})
}

func TestGetLogs(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{
		"eth_getLogs": `[{
			"address":"0x5FbDB2315678afecb367f032d93F642f64180aa3",
			"topics":["0x1111111111111111111111111111111111111111111111111111111111111111"],
			"data":"0x","transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa",
			"blockNumber":"0x2"
		}]`,
	})

	addr, err := HexToAddress("0x5FbDB2315678afecb367f032d93F642f64180aa3")
	require.NoError(t, err)
	topic, err := HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	require.NoError(t, err)

	logs, err := c.GetLogs(context.Background(), LogFilter{
		Address: addr, FromBlock: 1, ToBlock: 100, Topics: [][]Hash{{topic}},
	})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, addr, logs[0].Address)
	assert.Equal(t, uint64(2), logs[0].BlockNumber)

	require.Len(t, *calls, 1)
	arg, ok := (*calls)[0].Params[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "0x1", arg["fromBlock"])
	assert.Equal(t, "0x64", arg["toBlock"])
}

// TestGetLogsWildcardTopic checks that an empty inner slice becomes a null (match-any) position,
// which is the eth_getLogs convention.
func TestGetLogsWildcardTopic(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{"eth_getLogs": `[]`})

	topic, err := HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	require.NoError(t, err)
	_, err = c.GetLogs(context.Background(), LogFilter{Topics: [][]Hash{{topic}, {}}})
	require.NoError(t, err)

	arg, ok := (*calls)[0].Params[0].(map[string]any)
	require.True(t, ok)
	topics, ok := arg["topics"].([]any)
	require.True(t, ok)
	require.Len(t, topics, 2)
	assert.Nil(t, topics[1], "an empty position must serialize as null to match any value")
}

func TestHexHelpers(t *testing.T) {
	t.Run("uint round trip", func(t *testing.T) {
		for _, v := range []uint64{0, 1, 42, 21000, 1 << 40} {
			got, err := parseHexUint(encodeHexUint(v))
			require.NoError(t, err)
			assert.Equal(t, v, got)
		}
	})

	t.Run("big round trip", func(t *testing.T) {
		// Compared with Cmp, not Equal: a parsed zero and big.NewInt(0) are numerically equal but
		// differ in internal representation (nil vs empty nat).
		for _, v := range []int64{0, 1, 1_000_000_000} {
			got, err := parseHexBig(encodeHexBig(big.NewInt(v)))
			require.NoError(t, err)
			assert.Zero(t, got.Cmp(big.NewInt(v)), "round trip of %d", v)
		}
	})

	t.Run("bytes round trip", func(t *testing.T) {
		got, err := decodeHexBytes(encodeHexBytes([]byte{0xde, 0xad}))
		require.NoError(t, err)
		assert.Equal(t, []byte{0xde, 0xad}, got)
	})

	t.Run("empty data decodes to nil", func(t *testing.T) {
		for _, s := range []string{"0x", "", "  "} {
			got, err := decodeHexBytes(s)
			require.NoError(t, err)
			assert.Empty(t, got)
		}
	})

	t.Run("malformed values are errors", func(t *testing.T) {
		_, err := parseHexUint("0x")
		require.Error(t, err)
		_, err = parseHexUint("0xzz")
		require.Error(t, err)
		_, err = parseHexBig("0x")
		require.Error(t, err)
		_, err = decodeHexBytes("0xodd")
		require.Error(t, err)
	})
}

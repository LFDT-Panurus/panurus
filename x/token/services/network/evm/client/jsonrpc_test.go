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

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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

// TestCallClassifiesReverts mirrors TestEstimateGasClassifiesReverts: eth_call can revert against a
// real node exactly as eth_estimateGas can, and a caller needs the same distinction between "the chain
// rejected this" and "the node failed to answer" for either one.
func TestCallClassifiesReverts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     int
		message  string
		reverted bool
	}{
		// Code 3 is EIP-1474's execution error, which is what geth, anvil and everything built on them
		// actually return for a revert. Captured from a live anvil node, custom-error data and all.
		{name: "geth/anvil code", code: 3, message: "execution reverted", reverted: true},
		{name: "anvil custom error", code: 3, message: "execution reverted: custom error 0xe74c68bb:", reverted: true},
		{name: "besu wording", code: -32000, message: "Execution reverted", reverted: true},
		{name: "elsewhere in the implementation range", code: -32015, message: "VM Exception: revert", reverted: true},
		{name: "another server error", code: -32000, message: "header not found", reverted: false},
		{name: "method not found", code: -32601, message: "method not found", reverted: false},
		{name: "a non-revert code 3", code: 3, message: "out of gas", reverted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newErrorServer(t, tc.code, tc.message)

			_, err := c.Call(context.Background(), Address{}, nil, "latest")
			require.Error(t, err)
			assert.Equal(t, tc.reverted, errors.Is(err, ErrExecutionReverted))
			assert.Contains(t, err.Error(), tc.message)
		})
	}
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

// newErrorServer starts a JSON-RPC server that answers every method with the given error code and
// message. Revert classification is decided from exactly those two fields, so a test that pins it
// has to be able to set both.
func newErrorServer(t *testing.T, code int, message string) *JSONRPCClient {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]any{"code": code, "message": message},
		})
		// assert, not require: this runs on the server goroutine, where FailNow would not stop the test.
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	// #nosec G107 -- httptest local test server URL
	c, err := NewJSONRPCClient(srv.URL, srv.Client())
	require.NoError(t, err)

	return c
}

// TestEstimateGasClassifiesReverts pins the one distinction the caller acts on: a node that executed
// the transaction and rejected it, versus a node that failed to answer.
//
// A revert is permanent. Re-sending reaches the same verdict and pays gas for it, so the submitter
// has to be able to tell the two apart by class rather than by matching strings itself. The wording
// differs between clients, which is exactly why the message is matched case-insensitively here and
// not compared for equality.
func TestEstimateGasClassifiesReverts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     int
		message  string
		reverted bool
	}{
		// Code 3 is EIP-1474's execution error, what geth and anvil actually use for a revert. This is
		// the case that matters most: misreading it makes the submitter tell the caller to retry a
		// transaction the chain has permanently rejected.
		{name: "geth/anvil code", code: 3, message: "execution reverted", reverted: true},
		{
			name:     "anvil custom error, as captured from a live node",
			code:     3,
			message:  "execution reverted: custom error 0xe74c68bb:",
			reverted: true,
		},
		{name: "besu wording", code: -32000, message: "Execution reverted", reverted: true},
		{
			name:     "revert with a reason string",
			code:     -32000,
			message:  "execution reverted: StalePublicParams",
			reverted: true,
		},
		// Another node picking a different value from the same reserved range.
		{name: "elsewhere in the implementation range", code: -32015, message: "VM Exception: revert", reverted: true},
		// Code 3 without a revert: the code alone must not be enough to condemn a transaction.
		{name: "a non-revert code 3", code: 3, message: "out of gas", reverted: false},
		// Same implementation-defined code, a different server-side failure. Classifying this as a
		// revert would make the submitter give up on a transaction the chain never judged.
		{name: "another server error", code: -32000, message: "header not found", reverted: false},
		{name: "method not found", code: -32601, message: "method not found", reverted: false},
		{name: "invalid params", code: -32602, message: "invalid argument 0", reverted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newErrorServer(t, tc.code, tc.message)

			_, err := c.EstimateGas(context.Background(), CallMsg{})
			require.Error(t, err)
			assert.Equal(t, tc.reverted, errors.Is(err, ErrExecutionReverted),
				"a caller decides whether to retry from this classification")
			// Either way the node's own words survive, because they are what a human debugs from.
			assert.Contains(t, err.Error(), tc.message)
		})
	}
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

// TestSuggestGasFeesWithoutMaxPriorityFee covers the node the test network actually runs on: Besu
// does not implement eth_maxPriorityFeePerGas, which is an extension rather than part of the
// JSON-RPC spec. The tip is then the part of the suggested gas price that sits above the base fee.
func TestSuggestGasFeesWithoutMaxPriorityFee(t *testing.T) {
	c, calls := newTestServer(t, map[string]string{
		"eth_gasPrice":         `"0x3b9aca07"`,            // 1 gwei + 7 wei
		"eth_getBlockByNumber": `{"baseFeePerGas":"0x7"}`, // 7 wei
	})

	fees, err := c.SuggestGasFees(context.Background())
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1_000_000_000), fees.MaxPriorityFeePerGas)
	assert.Equal(t, big.NewInt(2*7+1_000_000_000), fees.MaxFeePerGas)

	methods := make([]string, 0, len(*calls))
	for _, call := range *calls {
		methods = append(methods, call.Method)
	}
	assert.Contains(t, methods, "eth_maxPriorityFeePerGas", "the node must be asked before falling back")
	assert.Contains(t, methods, "eth_gasPrice")
}

// TestSuggestGasFeesOnAZeroFeeChain covers a development chain that gives gas away: the suggested
// price does not exceed the base fee, so there is no tip, and that is not an error.
func TestSuggestGasFeesOnAZeroFeeChain(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_gasPrice":         `"0x0"`,
		"eth_getBlockByNumber": `{"baseFeePerGas":"0x0"}`,
	})

	fees, err := c.SuggestGasFees(context.Background())
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(0), fees.MaxPriorityFeePerGas)
	assert.Equal(t, big.NewInt(0), fees.MaxFeePerGas)
}

// TestSuggestGasFeesErrorsOnANullBlock covers a node that returns no block at all for "latest" (a
// gateway hiccup or a malformed response), which must not be treated as the pre-London zero-base-fee
// case: both look like an empty BaseFeePerGas field unless the two are told apart.
func TestSuggestGasFeesErrorsOnANullBlock(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_maxPriorityFeePerGas": `"0x1"`,
		"eth_getBlockByNumber":     `null`,
	})

	_, err := c.SuggestGasFees(context.Background())
	require.Error(t, err)
}

// TestSuggestGasFeesSurfacesRealFailures checks the fallback is only for an unimplemented method: a
// node that implements neither is a failure rather than a silent zero fee.
func TestSuggestGasFeesSurfacesRealFailures(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{"eth_getBlockByNumber": `{"baseFeePerGas":"0x7"}`})

	_, err := c.SuggestGasFees(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eth_gasPrice")
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

// TestGetLogsCapturesRemoved checks a log the node marks as reorged-out decodes with Removed set,
// rather than being indistinguishable from a canonical one.
func TestGetLogsCapturesRemoved(t *testing.T) {
	c, _ := newTestServer(t, map[string]string{
		"eth_getLogs": `[{
			"address":"0x5FbDB2315678afecb367f032d93F642f64180aa3",
			"topics":["0x1111111111111111111111111111111111111111111111111111111111111111"],
			"data":"0x","transactionHash":"0x853f272fffc6efc284fc16a254decca742d2347e05703e501c59968f78f81ffa",
			"blockNumber":"0x2","removed":true
		}]`,
	})

	logs, err := c.GetLogs(context.Background(), LogFilter{})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.True(t, logs[0].Removed, "a reorged-out log must be reported as such")
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

	t.Run("an uppercase 0X prefix is tolerated exactly like decodeHex accepts one", func(t *testing.T) {
		got, err := parseHexUint("0X2a")
		require.NoError(t, err)
		assert.Equal(t, uint64(42), got)

		gotBig, err := parseHexBig("0X2a")
		require.NoError(t, err)
		assert.Zero(t, gotBig.Cmp(big.NewInt(42)))

		gotBytes, err := decodeHexBytes("0Xdead")
		require.NoError(t, err)
		assert.Equal(t, []byte{0xde, 0xad}, gotBytes)
	})
}

// TestResponseBodyIsBounded checks the client refuses a response larger than its cap instead of
// buffering whatever the node decides to send. http.Client.Timeout bounds how long a response may
// take, not how large it may be, so without the cap a node streaming an endless body makes the driver
// allocate until it dies.
func TestResponseBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x`))
		// Streamed rather than built up front: the point is that the client stops reading, so the test
		// must not depend on the whole body being materialised anywhere.
		chunk := make([]byte, 1024)
		for i := range chunk {
			chunk[i] = '0'
		}
		for range 64 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewJSONRPCClient(srv.URL, srv.Client())
	require.NoError(t, err)
	c.maxResponse = 4096

	_, err = c.ChainID(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the 4096 byte limit")
}

// TestResponseAtTheBoundIsAccepted checks the cap rejects only what is genuinely over it, so a large
// but legitimate answer (getPublicParameters is the real one) still decodes.
func TestResponseAtTheBoundIsAccepted(t *testing.T) {
	big := make([]byte, 2048)
	for i := range big {
		big[i] = 'a'
	}
	c, _ := newTestServer(t, map[string]string{"eth_chainId": `"0x7a69"`})
	c.maxResponse = len(big)

	id, err := c.ChainID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(31337), id.Int64())
}

// headSequenceServer serves eth_getBlockByNumber from a scripted sequence of raw results, so a test
// can express "the head is missing, then it is there". Once the sequence runs out the last entry
// repeats, which is what makes a persistently null head expressible. Everything else answers a fixed
// tip so the fee derivation itself stays out of the way.
func headSequenceServer(t *testing.T, heads ...string) (*JSONRPCClient, *int) {
	t.Helper()
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&req)) {
			w.WriteHeader(http.StatusBadRequest)

			return
		}
		result := `"0x1"`
		if req.Method == "eth_getBlockByNumber" {
			result = heads[min(calls, len(heads)-1)]
			calls++
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

// TestSuggestGasFeesRetriesANullHead pins the reason the retry exists. "latest" is not a fixed block,
// and a node whose head is moving may answer that it has no block for it just then. That travels up
// as ErrNetworkUnavailable and aborts a whole token transaction, so a condition that clears by itself
// within a block time must not be reported on the first look.
func TestSuggestGasFeesRetriesANullHead(t *testing.T) {
	c, calls := headSequenceServer(t, `null`, `{"baseFeePerGas":"0x7"}`)

	fees, err := c.SuggestGasFees(context.Background())
	require.NoError(t, err, "a head that is momentarily absent must not fail the caller")
	assert.Equal(t, big.NewInt(2*7+1), fees.MaxFeePerGas)
	assert.Equal(t, 2, *calls, "it should have looked again exactly once")
}

// TestSuggestGasFeesGivesUpOnAPersistentlyNullHead keeps the retry bounded: a node that is genuinely
// not serving its head still has to fail, rather than the driver spinning on it.
func TestSuggestGasFeesGivesUpOnAPersistentlyNullHead(t *testing.T) {
	c, calls := headSequenceServer(t, `null`)

	_, err := c.SuggestGasFees(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned no block")
	assert.Equal(t, maxHeadReadAttempts, *calls, "the retry has to stop, not spin")
}

// TestSuggestGasFeesStopsWaitingOnACancelledContext covers the caller giving up mid-retry. A delay
// that ignored the context would hold a cancelled caller for the full wait.
func TestSuggestGasFeesStopsWaitingOnACancelledContext(t *testing.T) {
	c, calls := headSequenceServer(t, `null`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.SuggestGasFees(ctx)
	require.Error(t, err)
	assert.LessOrEqual(t, *calls, 1, "a cancelled caller should not be kept waiting through the retries")
}

// TestSendRawTransactionClassifiesARefusal pins the split that matters at broadcast time: a node that
// judged the transaction and refused it is permanent, and a transaction that never entered the pool
// consumed no nonce.
func TestSendRawTransactionClassifiesARefusal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		message  string
		rejected bool
	}{
		{"insufficient funds", "insufficient funds for gas * price + value", true},
		{"intrinsic gas too low", "intrinsic gas too low", true},
		{"exceeds block gas limit", "exceeds block gas limit", true},
		{"oversized data", "oversized data", true},
		// The nonce cases stay retryable: they say the local nonce is wrong rather than the
		// transaction is, and re-reading it from the chain is what fixes them.
		{"nonce too low", "nonce too low", false},
		{"nonce too high", "nonce too high", false},
		{"already known", "already known", false},
		{"replacement underpriced", "replacement transaction underpriced", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestServer(t, nil)
			c.endpoint = errorServer(t, -32000, tc.message)

			_, err := c.SendRawTransaction(context.Background(), []byte{0x02, 0x01})
			require.Error(t, err)
			assert.Equal(t, tc.rejected, errors.Is(err, ErrTransactionRejected),
				"a refusal the node will repeat is permanent; one about the nonce is not")
			assert.Contains(t, err.Error(), tc.message, "the node's own words are what a human debugs from")
		})
	}
}

// errorServer starts a server that answers every request with one JSON-RPC error.
func errorServer(t *testing.T, code int, message string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"error": map[string]any{"code": code, "message": message},
		})
		assert.NoError(t, err)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

// TestCodeAt covers the read the deployment check depends on: an address with a contract reports its
// code, and one without reports nothing rather than failing.
func TestCodeAt(t *testing.T) {
	addr, err := HexToAddress("0x00000000000000000000000000000000deadbeef")
	require.NoError(t, err)

	t.Run("a deployed contract reports its code", func(t *testing.T) {
		c, calls := newTestServer(t, map[string]string{"eth_getCode": `"0x6080604052"`})

		code, err := c.CodeAt(context.Background(), addr, BlockTagLatest)
		require.NoError(t, err)
		assert.Equal(t, []byte{0x60, 0x80, 0x60, 0x40, 0x52}, code)
		require.Len(t, *calls, 1)
		assert.Equal(t, []any{addr.Hex(), BlockTagLatest}, (*calls)[0].Params)
	})

	t.Run("an address with no contract reports nothing", func(t *testing.T) {
		c, _ := newTestServer(t, map[string]string{"eth_getCode": `"0x"`})

		code, err := c.CodeAt(context.Background(), addr, BlockTagLatest)
		require.NoError(t, err, "an address with no code is an answer, not a failure")
		assert.Empty(t, code)
	})
}

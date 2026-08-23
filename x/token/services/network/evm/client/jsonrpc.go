/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// DefaultRequestTimeout bounds a single JSON-RPC round trip when the caller's context has no
// deadline of its own.
const DefaultRequestTimeout = 30 * time.Second

// maxHeadReadAttempts bounds the retry baseFee performs when the node reports no block for "latest".
const maxHeadReadAttempts = 3

// headReadRetryDelay is how long baseFee waits before re-reading a head that was momentarily absent.
// It is well under a block time on every chain the driver targets, so the retries stay inside the
// window where the head is expected to settle rather than stretching the caller's deadline.
const headReadRetryDelay = 100 * time.Millisecond

// maxResponseBytes bounds the JSON-RPC response body this client will buffer.
//
// http.Client.Timeout bounds how long a response may take, not how large it may be, so without this a
// node that streams an endless body makes the driver allocate until it dies. The node is ordinarily
// operator-run infrastructure rather than an attacker, so this is defence in depth rather than a
// closed hole; it is the same bound statedelta.Validate already puts on the other input the driver
// reads from somebody else, and it costs nothing when the node behaves.
//
// The cap is set well above any legitimate answer. The largest of those by far is
// getPublicParameters, whose bytes arrive ABI-encoded and then hex-encoded, so roughly twice their
// size on the wire; 64 MiB leaves room for parameters an order of magnitude larger than the shipped
// drivers produce.
const maxResponseBytes = 64 << 20

// JSONRPCClient is the EVMClient implementation over HTTP JSON-RPC. It speaks plain eth_* methods,
// so it works against any standard node (Besu is the acceptance backend, anvil the inner loop, and
// an fabric-x-evm gateway is just another endpoint).
//
// It is deliberately dependency-free: the requests and the hex-quantity encoding the Ethereum JSON-RPC
// spec mandates are handled here rather than pulled from go-ethereum, whose license is a hard blocker
// (design §9, §15.6).
type JSONRPCClient struct {
	endpoint string
	http     *http.Client
	// id is the JSON-RPC request counter, incremented atomically so concurrent calls never reuse an id.
	id atomic.Uint64
	// maxResponse is the response-body bound, held as a field rather than read from the constant so a
	// test can exercise the limit without moving 64 MiB over the loopback interface.
	maxResponse int
}

// Compile-time assertion that the client satisfies the frozen interface.
var _ EVMClient = (*JSONRPCClient)(nil)

// NewJSONRPCClient returns a client for the node at endpoint. A nil httpClient uses a default one
// with DefaultRequestTimeout.
func NewJSONRPCClient(endpoint string, httpClient *http.Client) (*JSONRPCClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("evm client: empty endpoint")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultRequestTimeout}
	}

	return &JSONRPCClient{endpoint: endpoint, http: httpClient, maxResponse: maxResponseBytes}, nil
}

// ChainID returns the chain id reported by the node.
func (c *JSONRPCClient) ChainID(ctx context.Context) (*big.Int, error) {
	var out string
	if err := c.call(ctx, "eth_chainId", &out); err != nil {
		return nil, err
	}

	return parseHexBig(out)
}

// Ping checks the node is reachable by asking for its chain id.
func (c *JSONRPCClient) Ping(ctx context.Context) error {
	_, err := c.ChainID(ctx)

	return err
}

// Call performs a read-only contract call at the given block tag. A revert is classified the same way
// EstimateGas classifies one (ErrExecutionReverted), so a caller that later adds a Call against a
// method capable of reverting does not have to rediscover this distinction: eth_call and
// eth_estimateGas fail the same way against the same node.
func (c *JSONRPCClient) Call(ctx context.Context, to Address, data []byte, blockTag string) ([]byte, error) {
	if blockTag == "" {
		blockTag = BlockTagFinalized
	}
	arg := map[string]any{"to": to.Hex(), "data": encodeHexBytes(data)}
	var out string
	rpcErr, err := c.invoke(ctx, "eth_call", &out, arg, blockTag)
	if rpcErr != nil {
		if isReverted(rpcErr) {
			return nil, errors.Wrapf(ErrExecutionReverted, "eth_call failed: %s", rpcErr.Message)
		}

		return nil, errors.Wrap(rpcErr, "eth_call failed")
	}
	if err != nil {
		return nil, err
	}

	return decodeHexBytes(out)
}

// GetLogs returns the logs matching the filter.
func (c *JSONRPCClient) GetLogs(ctx context.Context, q LogFilter) ([]Log, error) {
	topics := make([]any, 0, len(q.Topics))
	for _, position := range q.Topics {
		if len(position) == 0 {
			topics = append(topics, nil) // matches any value at this position

			continue
		}
		values := make([]string, 0, len(position))
		for _, t := range position {
			values = append(values, t.Hex())
		}
		topics = append(topics, values)
	}
	toBlock := any(encodeHexUint(q.ToBlock))
	switch {
	case q.ToBlockTag != "":
		toBlock = q.ToBlockTag
	case q.ToBlock == 0:
		toBlock = BlockTagLatest
	}
	arg := map[string]any{
		"address":   q.Address.Hex(),
		"fromBlock": encodeHexUint(q.FromBlock),
		"toBlock":   toBlock,
	}
	if len(topics) != 0 {
		arg["topics"] = topics
	}

	var raw []jsonLog
	if err := c.call(ctx, "eth_getLogs", &raw, arg); err != nil {
		return nil, err
	}
	out := make([]Log, 0, len(raw))
	for i := range raw {
		l, err := raw[i].toLog()
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}

	return out, nil
}

// PendingNonceAt returns the next nonce for account, including pending transactions.
func (c *JSONRPCClient) PendingNonceAt(ctx context.Context, account Address) (uint64, error) {
	var out string
	if err := c.call(ctx, "eth_getTransactionCount", &out, account.Hex(), "pending"); err != nil {
		return 0, err
	}

	return parseHexUint(out)
}

// EstimateGas estimates the gas needed to execute msg.
func (c *JSONRPCClient) EstimateGas(ctx context.Context, msg CallMsg) (uint64, error) {
	arg := map[string]any{}
	if msg.From != nil {
		arg["from"] = msg.From.Hex()
	}
	if msg.To != nil {
		arg["to"] = msg.To.Hex()
	}
	if len(msg.Data) != 0 {
		arg["data"] = encodeHexBytes(msg.Data)
	}
	if msg.Value != nil {
		arg["value"] = encodeHexBig(msg.Value)
	}

	var out string
	rpcErr, err := c.invoke(ctx, "eth_estimateGas", &out, arg)
	if rpcErr != nil {
		// Classified here, where the code and message are still in hand: once wrapped, a caller can
		// only tell a rejected transaction from an unhappy node by reading strings.
		if isReverted(rpcErr) {
			return 0, errors.Wrapf(ErrExecutionReverted, "eth_estimateGas failed: %s", rpcErr.Message)
		}

		return 0, errors.Wrap(rpcErr, "eth_estimateGas failed")
	}
	if err != nil {
		return 0, err
	}

	return parseHexUint(out)
}

// errMethodNotFound is the JSON-RPC code for a method the node does not implement.
const errMethodNotFound = -32601

// errExecutionReverted is EIP-1474's "execution error" code, which is what geth, anvil and everything
// built on them return for a reverted call, with the revert data attached.
const errExecutionReverted = 3

// errServerMin and errServerMax bound the range JSON-RPC reserves for implementation-defined errors.
// Besu reports a revert as -32000 inside it, and other nodes pick other values in the same range, so
// the whole range is accepted and paired with the message rather than any single code being trusted
// alone.
const (
	errServerMin = -32099
	errServerMax = -32000
)

// ErrExecutionReverted marks the node reporting that the call it was given reverts against current
// state, as opposed to failing to answer at all.
//
// The difference is the whole point. A reverted eth_call or eth_estimateGas is the node executing the
// transaction and rejecting it, so sending it would reach the same verdict and only pay gas to be
// told again: it is permanent. Every other failure of the same call - unreachable node, timeout,
// malformed response - says nothing about the transaction and has to be retried instead.
var ErrExecutionReverted = errors.New("execution reverted")

// isReverted reports whether a JSON-RPC error is a revert.
//
// Getting this wrong is not cosmetic. A revert is the chain rejecting the transaction, which is
// permanent and has to be re-derived; everything else says nothing about the transaction and has to be
// retried. The submitter maps the two onto ErrTransactionReverted and ErrNetworkUnavailable, so a
// revert this function misses is handed to the caller as "retry with backoff" for a transaction that
// will be rejected identically every time.
//
// Nodes disagree on both halves of the answer. Besu reports a revert as -32000, inside the range
// JSON-RPC reserves for implementations; geth and anvil report it as EIP-1474's code 3. The wording
// differs too ("execution reverted" on geth, "Execution reverted" on Besu, and anvil appends the
// custom-error selector). So the code is accepted from either the reserved range or 3, and is always
// paired with the message, which keeps a non-revert failure carrying one of those codes ("out of gas"
// on 3, "header not found" on -32000) out of the permanent class.
func isReverted(err *rpcError) bool {
	if err == nil || !strings.Contains(strings.ToLower(err.Message), "revert") {
		return false
	}

	return err.Code == errExecutionReverted || (err.Code >= errServerMin && err.Code <= errServerMax)
}

// SuggestGasFees returns the node's suggested EIP-1559 fees: the priority tip it reports, and a max
// fee of baseFee*2 + tip, the customary headroom that keeps a transaction includable across a few
// blocks of base-fee growth.
func (c *JSONRPCClient) SuggestGasFees(ctx context.Context) (GasFees, error) {
	baseFee, err := c.baseFee(ctx)
	if err != nil {
		return GasFees{}, err
	}
	tip, err := c.suggestTip(ctx, baseFee)
	if err != nil {
		return GasFees{}, err
	}

	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	return GasFees{MaxFeePerGas: maxFee, MaxPriorityFeePerGas: tip}, nil
}

// baseFee reads the base fee of the latest block. A chain with no base fee at all (a pre-London or
// zero-fee configuration) reports the field absent, which is a base fee of zero rather than an error.
// A null result, by contrast, means the node did not return a block at all: head is a pointer so that
// case is distinguishable, since unmarshaling JSON null into a non-pointer target is a silent no-op
// that would otherwise be indistinguishable from the legitimate absent-field case.
//
// A null head is retried rather than reported straight away. "latest" is not a fixed block: it is
// whatever the node's head is at the instant it looks, and a node whose head is moving underneath the
// request is entitled to answer that it has no block for that tag just then. The condition clears by
// itself within a block time, but the caller cannot treat it that way, because this error travels up
// as ErrNetworkUnavailable and aborts the whole token transaction. Two of those aborts were observed
// against Besu during the integration suites, each one killing a transaction over a condition that
// would have resolved on the next call.
//
// The retry is bounded, so a node that is genuinely not serving its head still fails, and it costs
// nothing when the node behaves: the loop returns on the first attempt unless the answer was null.
func (c *JSONRPCClient) baseFee(ctx context.Context) (*big.Int, error) {
	for attempt := range maxHeadReadAttempts {
		var head *struct {
			BaseFeePerGas string `json:"baseFeePerGas"`
		}
		if err := c.call(ctx, "eth_getBlockByNumber", &head, "latest", false); err != nil {
			return nil, err
		}
		if head != nil {
			if head.BaseFeePerGas == "" {
				return new(big.Int), nil
			}

			return parseHexBig(head.BaseFeePerGas)
		}
		if attempt == maxHeadReadAttempts-1 {
			break
		}
		if err := wait(ctx, headReadRetryDelay); err != nil {
			return nil, err
		}
	}

	return nil, errors.Errorf(
		"evm client: eth_getBlockByNumber(\"latest\") returned no block in %d attempts", maxHeadReadAttempts)
}

// wait sleeps for d, or returns early if the context is done first. A retry that ignored the context
// would keep a cancelled caller waiting for the full delay.
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "evm client: interrupted while waiting to re-read the chain head")
	case <-timer.C:
		return nil
	}
}

// suggestTip asks the node what priority fee to pay.
//
// eth_maxPriorityFeePerGas is a de-facto extension rather than part of the JSON-RPC spec, and nodes
// are entitled not to implement it: Besu does not, in the configuration the test network runs. When
// it is missing, the tip is derived from eth_gasPrice, which every node implements, by taking the
// part of the suggested price that sits above the base fee. That is the same quantity, computed from
// what the node will tell us.
func (c *JSONRPCClient) suggestTip(ctx context.Context, baseFee *big.Int) (*big.Int, error) {
	var tipHex string
	supported, err := c.callOptional(ctx, "eth_maxPriorityFeePerGas", &tipHex)
	if err != nil {
		return nil, err
	}
	if supported {
		return parseHexBig(tipHex)
	}

	var priceHex string
	if err := c.call(ctx, "eth_gasPrice", &priceHex); err != nil {
		return nil, errors.Wrap(err, "node implements neither eth_maxPriorityFeePerGas nor eth_gasPrice")
	}
	price, err := parseHexBig(priceHex)
	if err != nil {
		return nil, err
	}

	// A suggested price at or below the base fee leaves nothing for the tip, which is legitimate: a
	// zero-fee dev chain reports a price of zero.
	if price.Cmp(baseFee) <= 0 {
		return new(big.Int), nil
	}

	return new(big.Int).Sub(price, baseFee), nil
}

// SendRawTransaction submits a signed, RLP-encoded transaction and returns its hash.
func (c *JSONRPCClient) SendRawTransaction(ctx context.Context, rawTx []byte) (Hash, error) {
	var out string
	if err := c.call(ctx, "eth_sendRawTransaction", &out, encodeHexBytes(rawTx)); err != nil {
		return Hash{}, err
	}

	return HexToHash(out)
}

// GetTransactionReceipt returns the receipt for txHash, or (nil, nil) if it is not yet mined.
func (c *JSONRPCClient) GetTransactionReceipt(ctx context.Context, txHash Hash) (*Receipt, error) {
	var raw *jsonReceipt
	if err := c.call(ctx, "eth_getTransactionReceipt", &raw, txHash.Hex()); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil // not mined yet
	}

	return raw.toReceipt()
}

// IsPending reports whether txHash is still pending. found is false when the node does not know the
// transaction at all (dropped or never seen), which the finality resolver uses to distinguish
// "pending" from "dropped" (design §7.1).
func (c *JSONRPCClient) IsPending(ctx context.Context, txHash Hash) (bool, bool, error) {
	var tx *struct {
		BlockNumber *string `json:"blockNumber"`
	}
	if err := c.call(ctx, "eth_getTransactionByHash", &tx, txHash.Hex()); err != nil {
		return false, false, err
	}
	if tx == nil {
		return false, false, nil // unknown to the node
	}

	// A null blockNumber means the node holds it in the mempool but it is not mined.
	return tx.BlockNumber == nil, true, nil
}

// --- JSON-RPC plumbing ---------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return "json-rpc error " + strconv.Itoa(e.Code) + ": " + e.Message
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// call performs one JSON-RPC request and unmarshals the result into out.
func (c *JSONRPCClient) call(ctx context.Context, method string, out any, params ...any) error {
	rpcErr, err := c.invoke(ctx, method, out, params...)
	if rpcErr != nil {
		return errors.Wrapf(rpcErr, "%s failed", method)
	}

	return err
}

// callOptional behaves like call, but reports separately whether the node implements the method at
// all. A caller that has a fallback can then take it without having to recover the JSON-RPC code from
// a wrapped error, and a genuine failure of a supported method is still returned as one.
func (c *JSONRPCClient) callOptional(
	ctx context.Context, method string, out any, params ...any,
) (supported bool, err error) {
	rpcErr, err := c.invoke(ctx, method, out, params...)
	if rpcErr != nil {
		if rpcErr.Code == errMethodNotFound {
			return false, nil
		}

		return true, errors.Wrapf(rpcErr, "%s failed", method)
	}

	return true, err
}

// invoke performs one JSON-RPC request and unmarshals the result into out. A JSON-RPC error response
// is returned unwrapped, as the first result, so callers can classify it by code; every other failure
// is returned as an ordinary error.
func (c *JSONRPCClient) invoke(ctx context.Context, method string, out any, params ...any) (*rpcError, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(&rpcRequest{JSONRPC: "2.0", ID: c.id.Add(1), Method: method, Params: params})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal %s request", method)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build %s request", method)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "%s call failed", method)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("%s call returned http %d", method, resp.StatusCode)
	}

	// Read through a bound rather than handing the decoder the body directly: one extra byte is
	// allowed so an over-long body is reported as such instead of arriving as a truncated,
	// syntactically broken document that reads like a node returning nonsense.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.maxResponse)+1))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read %s response", method)
	}
	if len(respBody) > c.maxResponse {
		return nil, errors.Errorf("%s response exceeds the %d byte limit", method, c.maxResponse)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, errors.Wrapf(err, "failed to decode %s response", method)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error, nil
	}
	if out == nil {
		return nil, nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal %s result", method)
	}

	return nil, nil
}

// --- wire types ----------------------------------------------------------------------------------

type jsonLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	TxHash      string   `json:"transactionHash"`
	BlockNumber string   `json:"blockNumber"`
	Removed     bool     `json:"removed"`
}

func (j *jsonLog) toLog() (Log, error) {
	addr, err := HexToAddress(j.Address)
	if err != nil {
		return Log{}, errors.Wrap(err, "invalid log address")
	}
	topics := make([]Hash, 0, len(j.Topics))
	for _, t := range j.Topics {
		h, err := HexToHash(t)
		if err != nil {
			return Log{}, errors.Wrap(err, "invalid log topic")
		}
		topics = append(topics, h)
	}
	data, err := decodeHexBytes(j.Data)
	if err != nil {
		return Log{}, errors.Wrap(err, "invalid log data")
	}
	txHash, err := HexToHash(j.TxHash)
	if err != nil {
		return Log{}, errors.Wrap(err, "invalid log transaction hash")
	}
	blockNumber, err := parseHexUint(j.BlockNumber)
	if err != nil {
		return Log{}, errors.Wrap(err, "invalid log block number")
	}

	return Log{
		Address: addr, Topics: topics, Data: data, TxHash: txHash, BlockNumber: blockNumber, Removed: j.Removed,
	}, nil
}

type jsonReceipt struct {
	TxHash      string    `json:"transactionHash"`
	BlockNumber *string   `json:"blockNumber"`
	Status      string    `json:"status"`
	Logs        []jsonLog `json:"logs"`
}

func (j *jsonReceipt) toReceipt() (*Receipt, error) {
	txHash, err := HexToHash(j.TxHash)
	if err != nil {
		return nil, errors.Wrap(err, "invalid receipt transaction hash")
	}
	status, err := parseHexUint(j.Status)
	if err != nil {
		return nil, errors.Wrap(err, "invalid receipt status")
	}
	out := &Receipt{TxHash: txHash, Status: status}
	if j.BlockNumber != nil {
		bn, err := parseHexUint(*j.BlockNumber)
		if err != nil {
			return nil, errors.Wrap(err, "invalid receipt block number")
		}
		out.BlockNumber = &bn
	}
	for i := range j.Logs {
		l, err := j.Logs[i].toLog()
		if err != nil {
			return nil, err
		}
		out.Logs = append(out.Logs, l)
	}

	return out, nil
}

// --- hex quantities ------------------------------------------------------------------------------

// encodeHexUint encodes a number as a minimal 0x-prefixed hex quantity, the JSON-RPC convention.
func encodeHexUint(x uint64) string {
	return "0x" + strconv.FormatUint(x, 16)
}

// encodeHexBig encodes a big.Int as a minimal 0x-prefixed hex quantity.
func encodeHexBig(x *big.Int) string {
	if x == nil {
		return "0x0"
	}

	return "0x" + x.Text(16)
}

// encodeHexBytes encodes a byte slice as 0x-prefixed hex data.
func encodeHexBytes(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}

// decodeHexBytes decodes 0x/0X-prefixed hex data, tolerating an empty or bare prefix value.
func decodeHexBytes(s string) ([]byte, error) {
	s = trimHexPrefix(s)
	if s == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid hex data [%s]", s)
	}

	return b, nil
}

// parseHexUint parses a 0x/0X-prefixed hex quantity into a uint64.
func parseHexUint(s string) (uint64, error) {
	trimmed := trimHexPrefix(s)
	if trimmed == "" {
		return 0, errors.Errorf("empty hex quantity")
	}
	v, err := strconv.ParseUint(trimmed, 16, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "invalid hex quantity [%s]", s)
	}

	return v, nil
}

// parseHexBig parses a 0x/0X-prefixed hex quantity into a big.Int.
//
// A JSON-RPC quantity is an unsigned integer, so a leading sign is rejected rather than parsed.
// big.Int.SetString accepts "-5" and "+5" happily, which would let a node hand back a negative base
// fee or tip; those flow into the signed transaction, where rlpBigInt encodes a value's magnitude and
// would drop the sign silently, signing a fee the driver did not compute. parseHexUint already
// refuses both, via strconv.ParseUint, so this only brings the big.Int path to the same strictness.
func parseHexBig(s string) (*big.Int, error) {
	trimmed := trimHexPrefix(s)
	if trimmed == "" {
		return nil, errors.Errorf("empty hex quantity")
	}
	if trimmed[0] == '-' || trimmed[0] == '+' {
		return nil, errors.Errorf("invalid hex quantity [%s]: quantities are unsigned", s)
	}
	v, ok := new(big.Int).SetString(trimmed, 16)
	if !ok {
		return nil, errors.Errorf("invalid hex quantity [%s]", s)
	}

	return v, nil
}

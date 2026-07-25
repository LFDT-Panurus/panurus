/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// perRequestTimeout bounds a single Ping HTTP round-trip when the caller's
// context has no earlier deadline.
const perRequestTimeout = 5 * time.Second

// rpcRequest is the JSON-RPC request body sent by Ping.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// rpcError is the error object of a JSON-RPC response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResponse is the JSON-RPC response body parsed by Ping.
type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int       `json:"id"`
	Result  string    `json:"result"`
	Error   *rpcError `json:"error"`
}

// parseHexChainID parses an Ethereum eth_chainId hex result string such as
// "0x1" or "0x7A69" (case-insensitive). It requires a "0x"/"0X" prefix and at
// least one hex digit, and rejects empty input, a bare prefix, non-hex
// characters, and values that overflow a uint64. It returns a wrapped error on
// failure.
func parseHexChainID(s string) (uint64, error) {
	if len(s) < 2 || (s[0:2] != "0x" && s[0:2] != "0X") {
		return 0, errors.Errorf("invalid chain id %q: missing 0x prefix", s)
	}
	digits := s[2:]
	if digits == "" {
		return 0, errors.Errorf("invalid chain id %q: no hex digits", s)
	}
	v, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "invalid chain id %q", s)
	}

	return v, nil
}

// Ping sends a JSON-RPC eth_chainId request to endpoint and returns the chain
// id reported by the node. It uses an http.Client whose timeout is derived from
// ctx (falling back to a short per-request timeout). A non-null JSON-RPC error
// object, a transport failure, or a malformed/invalid response all yield a
// wrapped error.
func Ping(ctx context.Context, endpoint string) (uint64, error) {
	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_chainId",
		Params:  []any{},
	})
	if err != nil {
		return 0, errors.Wrapf(err, "failed to marshal eth_chainId request")
	}

	//nolint:gosec // G704: endpoint is a trusted, harness-supplied EVM node URL (integration tests), not attacker-controlled input.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return 0, errors.Wrapf(err, "failed to build request for %s", endpoint)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: requestTimeout(ctx)}
	//nolint:gosec // G704: endpoint is a trusted, harness-supplied EVM node URL (integration tests), not attacker-controlled input.
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, errors.Wrapf(err, "eth_chainId request to %s failed", endpoint)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, errors.Errorf("eth_chainId request to %s returned status %d", endpoint, resp.StatusCode)
	}

	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, errors.Wrapf(err, "failed to decode eth_chainId response from %s", endpoint)
	}

	if out.Error != nil {
		return 0, errors.Errorf("eth_chainId returned JSON-RPC error (code %d): %s", out.Error.Code, out.Error.Message)
	}

	id, err := parseHexChainID(strings.TrimSpace(out.Result))
	if err != nil {
		return 0, errors.Wrapf(err, "eth_chainId response from %s", endpoint)
	}

	return id, nil
}

// requestTimeout returns the per-request HTTP timeout: the time remaining until
// the context deadline when one is set and sooner than perRequestTimeout,
// otherwise perRequestTimeout.
func requestTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < perRequestTimeout {
			return remaining
		}
	}

	return perRequestTimeout
}

// WaitReachable polls the JSON-RPC endpoint until it answers with an acceptable
// chain id or the wait is exhausted. It applies cfg.WithDefaults() and probes
// every PollInterval. It returns nil once Ping succeeds and either
// expectedChainID is 0 or the returned id matches expectedChainID. It returns a
// wrapped error when StartTimeout elapses or ctx is cancelled, including the
// last Ping error or a chain-id-mismatch message. ctx cancellation is honored
// promptly.
func WaitReachable(ctx context.Context, endpoint string, expectedChainID uint64, cfg Config) error {
	cfg = cfg.WithDefaults()

	waitCtx, cancel := context.WithTimeout(ctx, cfg.StartTimeout)
	defer cancel()

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		id, err := Ping(waitCtx, endpoint)
		switch {
		case err != nil:
			lastErr = err
		case expectedChainID != 0 && id != expectedChainID:
			lastErr = errors.Errorf("chain id mismatch: got %d, expected %d", id, expectedChainID)
		default:
			return nil
		}

		select {
		case <-waitCtx.Done():
			return errors.Wrapf(lastErr, "endpoint %s not reachable within %s (ctx: %v)", endpoint, cfg.StartTimeout, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

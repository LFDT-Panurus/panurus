/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Node is the chain a TMS settles on, provisioned per backend and hiding the concrete Besu/gateway node.
type Node interface {
	// Endpoint returns the node's JSON-RPC URL as reachable from the host.
	Endpoint() string
	// ChainID returns the chain the node is running.
	ChainID() int64
	// Stop tears the node down.
	Stop(ctx context.Context) error
}

// Besu already satisfies Node; this assertion keeps that true without touching Besu.
var _ Node = (*Besu)(nil)

// removeContainer tears down a node that came up but never became usable.
//
// It runs on its own short-lived context rather than the caller's: the usual reason a node is being
// removed here is that waiting for it ran out of time, and reusing the context that just expired
// would leave the container behind exactly when it most needs removing. Failure is logged rather than
// returned, because the caller is already reporting why the node is unusable and that is the more
// useful error of the two.
func removeContainer(node Node) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := node.Stop(ctx); err != nil {
		logger.Warnf("could not remove the unready evm container: %v", err)
	}
}

// answersChainID reports whether the node listening on the local port answers an eth_chainId request
// at endpoint with 200 OK.
func answersChainID(ctx context.Context, client *http.Client, port int, endpoint string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

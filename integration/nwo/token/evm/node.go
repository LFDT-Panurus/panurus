/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
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

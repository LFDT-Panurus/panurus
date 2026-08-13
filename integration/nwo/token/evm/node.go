/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import "context"

// Node is the chain a TMS settles on, provisioned per backend. The handler only ever needs the
// endpoint to reach it, the chain id to sign for it, and a way to tear it down, so the concrete
// backend (Besu by default, the fabric-x-evm gateway otherwise) is hidden behind this interface.
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

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token/evm/gateway"
)

// defaultGatewayImage is the published fabric-x-evm image booted when none is configured. It is the
// first release that ships the self-contained testnode mode (--listen/--chain-id, embedded Hardhat
// accounts); docker pulls it on demand, so no local build is needed.
const defaultGatewayImage = "ghcr.io/hyperledger/fabric-x-evm:0.1.3"

// defaultGatewayChainID is the chain id the gateway runs when none is configured. It mirrors the
// gateway testnode's own default, so the value gatewayNode reports back (and that a node's config is
// rendered with) matches the chain the container actually runs. Reporting an unset (zero) chain id
// makes the funder reject its transactions with "chain id must be set and positive".
const defaultGatewayChainID int64 = 31337

// gatewayNode adapts a gateway.Node to the Node interface. The gateway node has no ChainID method,
// since the chain id is an input to the boot rather than something it reports, so it is carried here.
type gatewayNode struct {
	inner   *gateway.Node
	chainID int64
}

// gatewayNode is a Node.
var _ Node = (*gatewayNode)(nil)

// Endpoint returns the node's JSON-RPC URL as reachable from the host.
func (n *gatewayNode) Endpoint() string { return n.inner.Endpoint() }

// ChainID returns the chain the node is running, as supplied at boot.
func (n *gatewayNode) ChainID() int64 { return n.chainID }

// Stop tears the node down.
func (n *gatewayNode) Stop(ctx context.Context) error { return n.inner.Stop(ctx) }

// startGatewayNode boots a fabric-x-evm gateway testnode on the given host port and wraps it as a
// Node. The image defaults to defaultGatewayImage when empty.
func startGatewayNode(ctx context.Context, image string, chainID int64, port int) (Node, error) {
	if image == "" {
		image = defaultGatewayImage
	}
	if chainID <= 0 {
		chainID = defaultGatewayChainID
	}

	node, err := gateway.StartTestnode(ctx, gateway.TestnodeOptions{
		Image:    image,
		ChainID:  uint64(chainID),
		HostPort: port,
	})
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to start the fabric-x-evm gateway node")
	}

	return &gatewayNode{inner: node, chainID: chainID}, nil
}

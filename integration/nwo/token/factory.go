/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	tevm "github.com/LFDT-Panurus/panurus/integration/nwo/token/evm"
	tfabric "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric/cc"
	fabricx2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
)

type platformFactory struct {
	ClientProvider fabricx2.ClientProvider
}

func NewPlatformFactory(clientProvider fabricx2.ClientProvider) *platformFactory {
	return &platformFactory{ClientProvider: clientProvider}
}

func (p *platformFactory) Name() string {
	return "token"
}

func (p *platformFactory) New(ctx api.Context, t api.Topology, builder api.Builder) api.Platform {
	tp := NewPlatform(ctx, t, builder)
	tp.AddNetworkHandler(fabric.TopologyName, tfabric.NewNetworkHandler(tp, builder, cc.NewDefaultGenericBackend(tp)))
	tp.AddNetworkHandler(fabricx.PlatformName, tfabric.NewNetworkHandler(tp, builder, &fabricx2.Backend{
		ClientProvider: p.ClientProvider,
	}))
	tp.AddNetworkHandler(tevm.TopologyName, tevm.NewNetworkHandler(tp, builder))

	// The fabric-x-evm gateway backend reuses the EVM handler, differing only in which node it boots:
	// NodeKind = GatewayTopologyName makes startNode launch the gateway container instead of Besu.
	gatewayHandler := tevm.NewNetworkHandler(tp, builder)
	gatewayHandler.NodeKind = tevm.GatewayTopologyName
	tp.AddNetworkHandler(tevm.GatewayTopologyName, gatewayHandler)

	return tp
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/chaguar"
	tevm "github.com/LFDT-Panurus/panurus/integration/nwo/token/evm"
	tfabric "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric/cc"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
	"github.com/onsi/gomega"
)

type platformFactory struct {
	ClientProvider chaguar.ClientProvider

	// chaguarBackends collects the chaguar backends created by New
	chaguarBackends []*chaguar.Backend
}

func NewPlatformFactory(clientProvider chaguar.ClientProvider) *platformFactory {
	return &platformFactory{ClientProvider: clientProvider}
}

func (p *platformFactory) Name() string {
	return "token"
}

func (p *platformFactory) New(ctx api.Context, t api.Topology, builder api.Builder) api.Platform {
	chaguarBackend := &chaguar.Backend{ClientProvider: p.ClientProvider}
	p.chaguarBackends = append(p.chaguarBackends, chaguarBackend)

	tp := NewPlatform(ctx, t, builder)
	tp.AddNetworkHandler(fabric.TopologyName, tfabric.NewNetworkHandler(tp, builder, cc.NewDefaultGenericBackend(tp)))
	tp.AddNetworkHandler(fabricx.PlatformName, tfabric.NewNetworkHandler(tp, builder, chaguarBackend))
	tp.AddNetworkHandler(tevm.TopologyName, tevm.NewNetworkHandler(tp, builder))

	// The gateway backend reuses the EVM handler; NodeKind makes startNode boot the gateway, not Besu.
	gatewayHandler := tevm.NewNetworkHandler(tp, builder)
	gatewayHandler.NodeKind = tevm.GatewayTopologyName
	tp.AddNetworkHandler(tevm.GatewayTopologyName, gatewayHandler)

	return tp
}

// InstallPendingPublicParams installs the public parameters that the chaguar backends recorded
// while the network was starting. It must be called once the network is fully up, from the same
// goroutine that started it, and it is a no-op for the backends that recorded nothing.
func (p *platformFactory) InstallPendingPublicParams() {
	for _, backend := range p.chaguarBackends {
		err := backend.InstallPendingPublicParams()
		gomega.Expect(err).ToNot(gomega.HaveOccurred(), "failed installing the pending public params")
	}
}

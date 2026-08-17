/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	tfabric "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token/fabric/cc"
	fabricx2 "github.com/LFDT-Panurus/panurus/integration/nwo/token/fabricx"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabricx"
)

type platformFactory struct {
	ClientProvider fabricx2.ClientProvider

	// fabricxBackends collects the fabricx backends created by New
	fabricxBackends []*fabricx2.Backend
}

func NewPlatformFactory(clientProvider fabricx2.ClientProvider) *platformFactory {
	return &platformFactory{ClientProvider: clientProvider}
}

func (p *platformFactory) Name() string {
	return "token"
}

func (p *platformFactory) New(ctx api.Context, t api.Topology, builder api.Builder) api.Platform {
	fabricxBackend := &fabricx2.Backend{ClientProvider: p.ClientProvider}
	p.fabricxBackends = append(p.fabricxBackends, fabricxBackend)

	tp := NewPlatform(ctx, t, builder)
	tp.AddNetworkHandler(fabric.TopologyName, tfabric.NewNetworkHandler(tp, builder, cc.NewDefaultGenericBackend(tp)))
	tp.AddNetworkHandler(fabricx.PlatformName, tfabric.NewNetworkHandler(tp, builder, fabricxBackend))

	return tp
}

// InstallPendingPublicParams installs the public parameters that the fabricx backends recorded
// while the network was starting. It must be called once the network is fully up, from the same
// goroutine that started it, and it is a no-op for the backends that recorded nothing.
func (p *platformFactory) InstallPendingPublicParams() {
	for _, backend := range p.fabricxBackends {
		backend.InstallPendingPublicParams()
	}
}

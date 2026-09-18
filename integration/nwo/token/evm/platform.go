/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	api2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/tedsuo/ifrit/grouper"
)

// Platform is the network-level platform for an EVM backend.
//
// It is deliberately almost empty. For Fabric the platform owns crypto material, config trees, peers
// and orderers; for EVM the chain is a single node and everything a TMS needs on it (the contracts,
// the endorser identities) is per-TMS work, which the token platform's network handler already does
// while generating its artifacts. Duplicating that here would mean bringing the chain up twice.
//
// It exists because the infrastructure resolves a platform for every topology it is given, so a
// topology with no platform is refused before anything runs.
type Platform struct {
	context  api2.Context
	topology *Topology
}

// Compile-time checks that the platform and its factories satisfy the infrastructure's contracts.
var (
	_ api2.Platform        = (*Platform)(nil)
	_ api2.PlatformFactory = (*PlatformFactory)(nil)
	_ api2.PlatformFactory = (*GatewayPlatformFactory)(nil)
)

// PlatformFactory builds the EVM platform for a topology.
type PlatformFactory struct{}

// NewPlatformFactory returns the factory to register with the integration infrastructure.
func NewPlatformFactory() *PlatformFactory { return &PlatformFactory{} }

// Name returns the platform type this factory serves.
func (f *PlatformFactory) Name() string { return TopologyName }

// New returns the platform for the given topology.
func (f *PlatformFactory) New(ctx api2.Context, t api2.Topology, _ api2.Builder) api2.Platform {
	topology, ok := t.(*Topology)
	if !ok {
		panic("evm nwo: the topology passed to the evm platform is not an evm topology")
	}

	return &Platform{context: ctx, topology: topology}
}

// GatewayPlatformFactory builds the EVM platform for a fabric-x-evm gateway topology (matched by Type = GatewayTopologyName).
type GatewayPlatformFactory struct{}

// NewGatewayPlatformFactory returns the platform factory for the fabric-x-evm gateway topology.
func NewGatewayPlatformFactory() *GatewayPlatformFactory { return &GatewayPlatformFactory{} }

// Name returns the platform type this factory serves.
func (f *GatewayPlatformFactory) Name() string { return GatewayTopologyName }

// New returns the platform for the given topology.
func (f *GatewayPlatformFactory) New(ctx api2.Context, t api2.Topology, _ api2.Builder) api2.Platform {
	topology, ok := t.(*Topology)
	if !ok {
		panic("evm nwo: the topology passed to the evm gateway platform is not an evm topology")
	}

	return &Platform{context: ctx, topology: topology}
}

// Name returns the network name.
func (p *Platform) Name() string { return p.topology.Name() }

// Type returns the platform type.
func (p *Platform) Type() string { return p.topology.Type() }

// GenerateConfigTree has nothing to write: an EVM network has no per-node chain configuration of its
// own, only the token configuration the token platform emits.
func (p *Platform) GenerateConfigTree() {}

// GenerateArtifacts has nothing to produce here. The chain and the contracts are brought up per TMS by
// the token network handler, which has to do it there anyway: a node's token configuration names the
// deployed contract addresses, so they must exist before that configuration is rendered.
func (p *Platform) GenerateArtifacts() {}

// Load has nothing to restore, since the platform holds no state of its own.
func (p *Platform) Load() {}

// Members returns no long-running processes: the chain runs in a container managed by the token
// network handler rather than as a member of the test network's process group.
func (p *Platform) Members() []grouper.Member { return nil }

// PostRun has nothing to do; the chain is already up by the time nodes start.
func (p *Platform) PostRun(bool) {}

// Cleanup is handled by the token network handler, which owns the container it started.
func (p *Platform) Cleanup() {}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

// GatewayTopologyName is the platform type a fabric-x-evm gateway-backed topology registers under.
// It selects the fabric-x-evm gateway node instead of the Besu default, while reusing the same EVM
// NetworkHandler: the two backends differ only in which node is booted, which the handler keys on
// through its NodeKind field.
const GatewayTopologyName = "fabricx-evm"

// NewGatewayTopology returns the topology for a fabric-x-evm gateway-backed network under the default
// network name. Its Type is GatewayTopologyName, so the token platform routes it to the gateway
// handler registered under that name.
func NewGatewayTopology() *Topology {
	return NewGatewayTopologyWithName(DefaultNetworkName)
}

// NewGatewayTopologyWithName returns the topology for a fabric-x-evm gateway-backed network under the
// given name, for a suite that stands up more than one.
func NewGatewayTopologyWithName(name string) *Topology {
	return &Topology{TopologyName: name, TopologyType: GatewayTopologyName}
}

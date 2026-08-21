/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

// GatewayTopologyName is the platform type a fabric-x-evm gateway-backed topology registers under.
const GatewayTopologyName = "fabricx-evm"

// NewGatewayTopology returns the fabric-x-evm gateway-backed topology under the default network name.
func NewGatewayTopology() *Topology {
	return NewGatewayTopologyWithName(DefaultNetworkName)
}

// NewGatewayTopologyWithName returns the fabric-x-evm gateway-backed topology under the given name.
func NewGatewayTopologyWithName(name string) *Topology {
	return &Topology{TopologyName: name, TopologyType: GatewayTopologyName}
}

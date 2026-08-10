/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

// Topology is the backend topology of an EVM-backed token network.
//
// It carries almost nothing, which is the point: unlike Fabric there are no organizations, channels,
// MSPs or peers to describe. What a node needs to reach the chain (the endpoint, the deployed
// contracts, its keys) is discovered when the network is generated and handed to it through its token
// configuration, so the topology only has to name itself and say which handler owns it.
type Topology struct {
	TopologyName string `yaml:"name,omitempty"`
	TopologyType string `yaml:"type,omitempty"`
}

// DefaultNetworkName is the name of the network an unnamed EVM topology stands up. It is the name
// the fabric and fabricx topologies give theirs too, and the suites address networks by it, so an
// EVM-backed network answering to a different one would be invisible to them.
const DefaultNetworkName = "default"

// NewTopology returns the topology for an EVM-backed network under the default network name.
func NewTopology() *Topology {
	return NewTopologyWithName(DefaultNetworkName)
}

// NewTopologyWithName returns the topology for an EVM-backed network under the given name, for a
// suite that stands up more than one.
func NewTopologyWithName(name string) *Topology {
	return &Topology{TopologyName: name, TopologyType: TopologyName}
}

// Name returns the network name. It is the name of this network instance, not of the technology
// backing it: Type says that, and the token platform routes on Type alone.
func (t *Topology) Name() string {
	if t.TopologyName == "" {
		return DefaultNetworkName
	}

	return t.TopologyName
}

// Type returns the platform type, which is how the token platform finds the handler for this network.
func (t *Topology) Type() string {
	if t.TopologyType == "" {
		return TopologyName
	}

	return t.TopologyType
}

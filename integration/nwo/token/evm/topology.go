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

// NewTopology returns the topology for an EVM-backed network.
func NewTopology() *Topology {
	return &Topology{TopologyName: TopologyName, TopologyType: TopologyName}
}

// Name returns the network name.
func (t *Topology) Name() string {
	if t.TopologyName == "" {
		return TopologyName
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

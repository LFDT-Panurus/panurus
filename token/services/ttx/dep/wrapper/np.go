/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package wrapper

import (
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
)

type NetworkProvider struct {
	np *network.Provider
}

func NewNetworkProvider(np *network.Provider) *NetworkProvider {
	return &NetworkProvider{np: np}
}

func (n *NetworkProvider) GetNetwork(network string, channel string) (dep.Network, error) {
	return n.np.GetNetwork(network, channel)
}

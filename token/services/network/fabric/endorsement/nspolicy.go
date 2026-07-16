/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// DiscoveryProvider discovers, via Fabric service discovery, the MSP IDs of the peers
// that must endorse to satisfy the endorsement policy of namespace on the given channel.
//
//go:generate counterfeiter -o mock/discovery_provider.go -fake-name DiscoveryProvider . DiscoveryProvider
type DiscoveryProvider interface {
	Discover(network, channel, namespace string) ([]string, error)
}

// NetworkDiscoveryProvider is the production DiscoveryProvider, backed by a live
// *fabric.NetworkServiceProvider.
type NetworkDiscoveryProvider struct {
	fnsp *fabric.NetworkServiceProvider
}

// NewNetworkDiscoveryProvider returns a new NetworkDiscoveryProvider.
func NewNetworkDiscoveryProvider(fnsp *fabric.NetworkServiceProvider) *NetworkDiscoveryProvider {
	return &NetworkDiscoveryProvider{fnsp: fnsp}
}

// Discover returns the set of MSP IDs of the peers that Fabric service discovery
// selected as satisfying the endorsement policy of namespace.
func (p *NetworkDiscoveryProvider) Discover(network, channel, namespace string) ([]string, error) {
	fns, err := p.fnsp.FabricNetworkService(network)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get fabric network service for [%s]", network)
	}
	ch, err := fns.Channel(channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get channel [%s] for network [%s]", channel, network)
	}

	peers, err := ch.Chaincode(namespace).Discover().Call()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to discover endorsers for namespace [%s]", namespace)
	}

	mspIDSet := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		mspIDSet[peer.MSPID] = struct{}{}
	}
	mspIDs := make([]string, 0, len(mspIDSet))
	for mspID := range mspIDSet {
		mspIDs = append(mspIDs, mspID)
	}

	return mspIDs, nil
}

// DiscoveryEndorserSelector selects, among the configured FSC endorsers, a random
// subset that satisfies the real endorsement policy of the target namespace, using
// Fabric service discovery to determine which MSPs must endorse.
type DiscoveryEndorserSelector struct {
	discoveryProvider DiscoveryProvider
	channelProvider   fsc.ChannelProvider
}

// NewDiscoveryEndorserSelector returns a new DiscoveryEndorserSelector.
func NewDiscoveryEndorserSelector(discoveryProvider DiscoveryProvider, channelProvider fsc.ChannelProvider) *DiscoveryEndorserSelector {
	return &DiscoveryEndorserSelector{discoveryProvider: discoveryProvider, channelProvider: channelProvider}
}

// SelectEndorsers returns a random subset of configured that satisfies the namespace's
// endorsement policy, as reported by Fabric service discovery.
func (s *DiscoveryEndorserSelector) SelectEndorsers(_ context.Context, tmsID token2.TMSID, configured []view.Identity) ([]view.Identity, error) {
	requiredMSPIDs, err := s.discoveryProvider.Discover(tmsID.Network, tmsID.Channel, tmsID.Namespace)
	if err != nil {
		return nil, err
	}
	if len(requiredMSPIDs) == 0 {
		return nil, errors.Errorf("discovery returned no endorsing MSPs for namespace [%s]", tmsID.Namespace)
	}

	mspManager, err := s.channelProvider.GetMSPManager(tmsID.Network, tmsID.Channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get msp manager for [%s:%s]", tmsID.Network, tmsID.Channel)
	}
	mspOf := func(id view.Identity) (string, error) { return mspManager.GetMSPIdentifier(id) }

	return fsc.SelectEndorsersForMSPSets(configured, mspOf, [][]string{requiredMSPIDs})
}

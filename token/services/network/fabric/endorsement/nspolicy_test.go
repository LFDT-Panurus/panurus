/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement"
	fscmock "github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryEndorserSelector_SelectEndorsers(t *testing.T) {
	tmsID := token.TMSID{Network: "test_network", Channel: "test_channel", Namespace: "test_namespace"}

	org1Endorser := view.Identity("org1-endorser")
	org2Endorser := view.Identity("org2-endorser")

	newMSPManager := func(idToMSP map[string]string) *fscmock.MSPManager {
		mspManager := &fscmock.MSPManager{}
		mspManager.GetMSPIdentifierStub = func(sid []byte) (string, error) {
			if mspID, ok := idToMSP[string(sid)]; ok {
				return mspID, nil
			}

			return "", errors.Errorf("unknown identity")
		}

		return mspManager
	}

	t.Run("success - single required MSP satisfied", func(t *testing.T) {
		discoveryProvider := &mock.DiscoveryProvider{}
		discoveryProvider.DiscoverReturns([]string{"Org1MSP"}, nil)

		channelProvider := &fscmock.ChannelProvider{}
		channelProvider.GetMSPManagerReturns(newMSPManager(map[string]string{
			string(org1Endorser): "Org1MSP",
			string(org2Endorser): "Org2MSP",
		}), nil)

		selector := endorsement.NewDiscoveryEndorserSelector(discoveryProvider, channelProvider)

		selected, err := selector.SelectEndorsers(context.Background(), tmsID, []view.Identity{org1Endorser, org2Endorser})

		require.NoError(t, err)
		require.Len(t, selected, 1)
		assert.Equal(t, org1Endorser.String(), selected[0].String())

		network, channel, namespace := discoveryProvider.DiscoverArgsForCall(0)
		assert.Equal(t, tmsID.Network, network)
		assert.Equal(t, tmsID.Channel, channel)
		assert.Equal(t, tmsID.Namespace, namespace)
	})

	t.Run("success - multiple required MSPs satisfied", func(t *testing.T) {
		discoveryProvider := &mock.DiscoveryProvider{}
		discoveryProvider.DiscoverReturns([]string{"Org1MSP", "Org2MSP"}, nil)

		channelProvider := &fscmock.ChannelProvider{}
		channelProvider.GetMSPManagerReturns(newMSPManager(map[string]string{
			string(org1Endorser): "Org1MSP",
			string(org2Endorser): "Org2MSP",
		}), nil)

		selector := endorsement.NewDiscoveryEndorserSelector(discoveryProvider, channelProvider)

		selected, err := selector.SelectEndorsers(context.Background(), tmsID, []view.Identity{org1Endorser, org2Endorser})

		require.NoError(t, err)
		assert.Len(t, selected, 2)
	})

	t.Run("failed - discovery error", func(t *testing.T) {
		discoveryProvider := &mock.DiscoveryProvider{}
		discoveryProvider.DiscoverReturns(nil, errors.New("discovery failed"))

		selector := endorsement.NewDiscoveryEndorserSelector(discoveryProvider, &fscmock.ChannelProvider{})

		_, err := selector.SelectEndorsers(context.Background(), tmsID, []view.Identity{org1Endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "discovery failed")
	})

	t.Run("failed - discovery returns no MSPs", func(t *testing.T) {
		discoveryProvider := &mock.DiscoveryProvider{}
		discoveryProvider.DiscoverReturns([]string{}, nil)

		selector := endorsement.NewDiscoveryEndorserSelector(discoveryProvider, &fscmock.ChannelProvider{})

		_, err := selector.SelectEndorsers(context.Background(), tmsID, []view.Identity{org1Endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "discovery returned no endorsing MSPs")
	})

	t.Run("failed - no configured endorser in required MSP", func(t *testing.T) {
		discoveryProvider := &mock.DiscoveryProvider{}
		discoveryProvider.DiscoverReturns([]string{"Org2MSP"}, nil)

		channelProvider := &fscmock.ChannelProvider{}
		channelProvider.GetMSPManagerReturns(newMSPManager(map[string]string{
			string(org1Endorser): "Org1MSP",
		}), nil)

		selector := endorsement.NewDiscoveryEndorserSelector(discoveryProvider, channelProvider)

		_, err := selector.SelectEndorsers(context.Background(), tmsID, []view.Identity{org1Endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no configured endorser covers")
	})

	t.Run("failed - GetMSPManager error", func(t *testing.T) {
		discoveryProvider := &mock.DiscoveryProvider{}
		discoveryProvider.DiscoverReturns([]string{"Org1MSP"}, nil)

		channelProvider := &fscmock.ChannelProvider{}
		channelProvider.GetMSPManagerReturns(nil, errors.New("no msp manager"))

		selector := endorsement.NewDiscoveryEndorserSelector(discoveryProvider, channelProvider)

		_, err := selector.SelectEndorsers(context.Background(), tmsID, []view.Identity{org1Endorser})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get msp manager")
	})
}

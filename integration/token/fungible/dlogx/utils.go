/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlogx

import (
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	pp2 "github.com/LFDT-Panurus/panurus/token/services/network/fabricx/pp"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils"
)

type PublicParamsInputs struct {
	FSCNodeIDs       []string
	PublicParameters pp2.PublicParameters
}

func GetPublicParamsInputs(ii *integration.Infrastructure) ([]PublicParamsInputs, error) {
	tp := find[api.Platform, *token.Platform](ii.NWO.Platforms)
	if tp == nil {
		return nil, errors.New("tp was nil")
	}
	ttp := find[api.Topology, *token.Topology](ii.Topologies)
	if ttp == nil {
		return nil, errors.New("ttp was nil")
	}
	if len(ttp.TMSs) == 0 {
		return nil, errors.New("no tmss found")
	}

	pps := make([]PublicParamsInputs, len(ttp.TMSs))
	for i, tms := range ttp.TMSs {
		nodeIDs := make([]string, len(tms.FSCNodes))
		for j, n := range tms.FSCNodes {
			nodeIDs[j] = n.Name
		}
		pps[i] = PublicParamsInputs{
			FSCNodeIDs: nodeIDs,
			PublicParameters: pp2.PublicParameters{
				TMSID: driver.TMSID{Network: tms.Network, Channel: tms.Channel, Namespace: tms.Namespace},
				Raw:   tp.PublicParameters(tms),
			},
		}
	}

	return pps, nil
}

func find[L any, K any](items []L) K {
	for _, item := range items {
		if typed, ok := any(item).(K); ok {
			return typed
		}
	}

	return utils.Zero[K]()
}

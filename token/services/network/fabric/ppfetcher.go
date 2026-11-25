/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"

	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/chaincode"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

// EndorserSelectionProvider resolves the endorser selection configured for a TMS.
type EndorserSelectionProvider = config3.EndorserSelectionProvider

type chaincodePublicParamsFetcher struct {
	viewManager       ViewManager
	selectionProvider EndorserSelectionProvider
}

// NewChaincodePublicParamsFetcher returns a fetcher that reads the public parameters from
// the token chaincode. selectionProvider constrains which peers may serve the query, so
// that a node configured for org affinity does not fall back to default discovery here.
func NewChaincodePublicParamsFetcher(viewManager *view.Manager, selectionProvider EndorserSelectionProvider) *chaincodePublicParamsFetcher {
	return &chaincodePublicParamsFetcher{viewManager: viewManager, selectionProvider: selectionProvider}
}

func (f *chaincodePublicParamsFetcher) Fetch(network driver2.Network, channel driver2.Channel, namespace driver2.Namespace) ([]byte, error) {
	qv := chaincode.NewQueryView(
		namespace,
		QueryPublicParamsFunction,
	).WithNetwork(network).WithChannel(channel)

	// A node that restricts endorsement to named organizations must not read the public
	// parameters from another org's peer either.
	selection, err := config3.SelectionFor(f.selectionProvider, network, channel, namespace)
	if err != nil {
		return nil, err
	}
	qv = config3.ApplyEndorserSelection(qv, selection)

	ppBoxed, err := f.viewManager.InitiateView(context.Background(), qv)
	if err != nil {
		if selection.IsSet() {
			// Name the restriction in force: a selection no peer satisfies otherwise
			// surfaces as a discovery failure that points at no configuration key.
			return nil, config3.NoUsableEndorserError(
				err,
				"query the public parameters of ["+network+":"+channel+":"+namespace+"]",
				selection.Explain(),
			)
		}

		return nil, err
	}

	pp, ok := ppBoxed.([]byte)
	if !ok {
		return pp, errors.Errorf("unexpected public params type %T, expected []byte", ppBoxed)
	}

	return pp, nil
}

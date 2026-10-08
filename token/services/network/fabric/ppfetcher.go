/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"

	tokenconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	config3 "github.com/LFDT-Panurus/panurus/token/services/network/fabric/config"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/chaincode"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

// EndorserSelectionProvider resolves the endorser selection configured for a TMS.
type EndorserSelectionProvider interface {
	// EndorserSelectionFor returns the endorser selection configured for the given
	// coordinates. A TMS that configures no preference yields the zero value.
	EndorserSelectionFor(network, channel, namespace string) (config3.EndorserSelection, error)
}

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
	selection, err := f.endorserSelection(network, channel, namespace)
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

// endorserSelection resolves the endorser selection configured for the given coordinates.
//
// A missing configuration is the one benign failure and yields the zero value — Fabric's
// default discovery: the public parameters of a namespace may genuinely have to be
// fetched before that namespace has a usable configuration, so a node that has not
// reached a resolvable state yet must still be able to bootstrap.
//
// Every other failure means the operator wrote a selection that could not be honoured,
// and is returned. This deliberately matches the endorse path rather than trading
// locality for availability: falling back would read the public parameters from the very
// peers the selection exists to avoid, and a node configured for org affinity is better
// served by a named error than by a query that quietly leaves its organization.
func (f *chaincodePublicParamsFetcher) endorserSelection(network, channel, namespace string) (config3.EndorserSelection, error) {
	if f.selectionProvider == nil {
		return config3.EndorserSelection{}, nil
	}

	selection, err := f.selectionProvider.EndorserSelectionFor(network, channel, namespace)
	switch {
	case err == nil:
		return selection, nil
	case errors.Is(err, tokenconfig.ErrConfigurationNotFound):
		logger.Debugf("no configuration for [%s:%s:%s] yet, using default discovery", network, channel, namespace)

		return config3.EndorserSelection{}, nil
	default:
		return config3.EndorserSelection{}, errors.WithMessagef(err, "failed resolving the endorser selection for [%s:%s:%s]", network, channel, namespace)
	}
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"context"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	driver2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/chaincode"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

type chaincodePublicParamsFetcher struct {
	viewManager ViewManager
}

func NewChaincodePublicParamsFetcher(viewManager *view.Manager) *chaincodePublicParamsFetcher {
	return &chaincodePublicParamsFetcher{viewManager: viewManager}
}

func (f *chaincodePublicParamsFetcher) Fetch(network driver2.Network, channel driver2.Channel, namespace driver2.Namespace) ([]byte, error) {
	ppBoxed, err := f.viewManager.InitiateView(context.Background(), chaincode.NewQueryView(
		namespace,
		QueryPublicParamsFunction,
	).WithNetwork(network).WithChannel(channel))
	if err != nil {
		return nil, err
	}

	pp, ok := ppBoxed.([]byte)
	if !ok {
		return pp, errors.Errorf("unexpected public params type %T, expected []byte", ppBoxed)
	}

	return pp, nil
}

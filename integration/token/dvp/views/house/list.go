/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package house

import (
	"encoding/json"

	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/assert"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// GetHouse contains the input to query a house by id
type GetHouse struct {
	HouseID string
}

type GetHouseView struct {
	*GetHouse
}

func (p *GetHouseView) Call(context view.Context) (any, error) {
	wallet := nfttx.MyWallet(context)
	assert.NotNil(wallet, "wallet is nil")

	house := &House{}
	assert.NoError(wallet.QueryByKey(context.Context(), house, "LinearID", p.HouseID), "failed loading house with id %s", p.HouseID)

	return house, nil
}

type GetHouseViewFactory struct{}

func (i *GetHouseViewFactory) NewView(in []byte) (view.View, error) {
	f := &GetHouseView{GetHouse: &GetHouse{}}
	err := json.Unmarshal(in, f.GetHouse)
	assert.NoError(err, "failed unmarshalling input")

	return f, nil
}

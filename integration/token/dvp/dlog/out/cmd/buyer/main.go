/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	fscnode "github.com/hyperledger-labs/fabric-smart-client/node"

	fdlog "github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	views1 "github.com/LFDT-Panurus/panurus/integration/token/common/views"
	views "github.com/LFDT-Panurus/panurus/integration/token/dvp/views"
	cash "github.com/LFDT-Panurus/panurus/integration/token/dvp/views/cash"
	house "github.com/LFDT-Panurus/panurus/integration/token/dvp/views/house"
	viewregistry "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

func main() {
	n := fscnode.New()
	n.InstallSDK(fdlog.NewSDK(n))

	n.Execute(func() error {
		registry := viewregistry.GetRegistry(n)
		if err := registry.RegisterFactory("queryHouse", &house.GetHouseViewFactory{}); err != nil {
			return err
		}
		if err := registry.RegisterFactory("balance", &views.BalanceViewFactory{}); err != nil {
			return err
		}
		if err := registry.RegisterFactory("balance", &views.BalanceViewFactory{}); err != nil {
			return err
		}
		if err := registry.RegisterFactory("TxFinality", &views1.TxFinalityViewFactory{}); err != nil {
			return err
		}
		registry.RegisterResponder(&cash.AcceptCashView{}, &cash.IssueCashView{})
		registry.RegisterResponder(&views.BuyHouseView{}, &views.SellHouseView{})

		return nil
	})
}

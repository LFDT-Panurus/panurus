/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	fscnode "github.com/hyperledger-labs/fabric-smart-client/node"

	viewregistry "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	fdlog "github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	views "github.com/LFDT-Panurus/panurus/integration/token/dvp/views"
	house "github.com/LFDT-Panurus/panurus/integration/token/dvp/views/house"
)

func main() {
	n := fscnode.New()
	n.InstallSDK(fdlog.NewSDK(n))

	n.Execute(func() error {
		registry := viewregistry.GetRegistry(n)
		if err := registry.RegisterFactory("sell", &views.SellHouseViewFactory{}); err != nil {
			return err
		}
		if err := registry.RegisterFactory("queryHouse", &house.GetHouseViewFactory{}); err != nil {
			return err
		}
		if err := registry.RegisterFactory("balance", &views.BalanceViewFactory{}); err != nil {
			return err
		}
		registry.RegisterResponder(&house.AcceptHouseView{}, &house.IssueHouseView{})

		return nil
	})
}

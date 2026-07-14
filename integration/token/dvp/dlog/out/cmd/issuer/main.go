/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	fscnode "github.com/hyperledger-labs/fabric-smart-client/node"

	viewregistry "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	fdlog "github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	cash "github.com/LFDT-Panurus/panurus/integration/token/dvp/views/cash"
)

func main() {
	n := fscnode.New()
	n.InstallSDK(fdlog.NewSDK(n))

	n.Execute(func() error {
		registry := viewregistry.GetRegistry(n)
		if err := registry.RegisterFactory("issue", &cash.IssueCashViewFactory{}); err != nil {
			return err
		}
		if err := registry.RegisterFactory("issued", &cash.ListIssuedTokensViewFactory{}); err != nil {
			return err
		}

		return nil
	})
}

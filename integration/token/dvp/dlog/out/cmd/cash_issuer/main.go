/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	fscnode "github.com/hyperledger-labs/fabric-smart-client/node"

	fdlog "github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fdlog"
	cash "github.com/LFDT-Panurus/panurus/integration/token/dvp/views/cash"
	viewregistry "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

func main() {
	n := fscnode.New()
	n.InstallSDK(fdlog.NewSDK(n))

	n.Execute(func() error {
		registry := viewregistry.GetRegistry(n)
		if err := registry.RegisterFactory("issue_cash", &cash.IssueCashViewFactory{}); err != nil {
			return err
		}

		return nil
	})
}

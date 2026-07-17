/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"github.com/LFDT-Panurus/panurus/integration/token/common/sdk/fxdlog"
	"github.com/LFDT-Panurus/panurus/integration/token/fungible/sdk/party"
	fscnode "github.com/hyperledger-labs/fabric-smart-client/node"
)

func main() {
	n := fscnode.New()
	if err := n.InstallSDK(party.NewFrom(fxdlog.NewSDK(n))); err != nil {
		panic(err)
	}
	n.Execute(func() error {
		return nil
	})
}

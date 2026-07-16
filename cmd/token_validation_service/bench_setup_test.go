/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/benchmark/node"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	fscnode "github.com/hyperledger-labs/fabric-smart-client/node"
	viewsdk "github.com/hyperledger-labs/fabric-smart-client/platform/view/sdk/dig"
	viewregistry "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
)

// generateConfigWS mirrors node.GenerateConfig but uses websocket transport.
// (node.GenerateConfig uses default LibP2P)
func generateConfigWS(testdataDir string) error {
	fscTopology := fsc.NewTopology()
	fscTopology.P2PCommunicationType = fsc.WebSocket
	fscTopology.SetLogging("error", "")
	fscTopology.AddNodeByName("test-node")

	_, err := integration.GenerateAt(8099, testdataDir, false, fscTopology)
	return err
}

// setupNodeP2P mirrors node.SetupNode and uses the default websocket transport.
func setupNodeP2P(confPath string, factories ...node.NamedFactory) (*fscnode.Node, error) {
	n := fscnode.NewWithConfPath(confPath)

	if err := n.InstallSDK(viewsdk.NewSDK(n)); err != nil {
		return nil, err
	}

	if err := n.Start(); err != nil {
		return nil, err
	}

	reg := viewregistry.GetRegistry(n)
	for _, f := range factories {
		if err := reg.RegisterFactory(f.Name, f.Factory); err != nil {
			return nil, err
		}
	}

	return n, nil
}

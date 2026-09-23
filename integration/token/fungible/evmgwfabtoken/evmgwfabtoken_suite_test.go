/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evmgwfabtoken

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd EVM Gateway FabToken Suite")
}

// StartPortEVMGatewayFabToken returns the port range this suite allocates from. It is its own range,
// so it can run side by side with the other EVM suites without their nodes colliding.
func StartPortEVMGatewayFabToken() int {
	return integration.EVMFungibleGatewayFabToken.StartPortForNode()
}

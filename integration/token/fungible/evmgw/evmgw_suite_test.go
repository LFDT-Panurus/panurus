/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evmgw

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd EVM Gateway Suite")
}

// StartPortEVMGateway returns the port range this suite allocates from. It is its own range rather
// than the Besu suite's, so the two can run side by side without their nodes colliding.
func StartPortEVMGateway() int {
	return integration.EVMFungibleGateway.StartPortForNode()
}

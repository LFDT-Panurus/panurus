/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evmfabtoken

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd EVM FabToken Suite")
}

var _ = AfterSuite(token2.CleanupBuildArtifacts)

// StartPortEVMFabToken returns the port range this suite allocates from. It is its own range rather
// than the zkatdlog suite's, so the two can run side by side without their nodes colliding.
func StartPortEVMFabToken() int {
	return integration.EVMFungibleFabToken.StartPortForNode()
}

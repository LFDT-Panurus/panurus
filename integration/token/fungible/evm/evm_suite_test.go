/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd EVM Suite")
}

var _ = AfterSuite(token2.CleanupBuildArtifacts)

// StartPortEVM returns the port range this suite allocates from.
func StartPortEVM() int {
	return integration.EVMFungible.StartPortForNode()
}

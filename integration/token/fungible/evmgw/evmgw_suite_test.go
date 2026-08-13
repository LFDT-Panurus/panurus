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

// StartPortEVMGateway returns the port range this suite allocates from. It shares the EVM fungible
// range with the Besu suite: the two suites never run in the same process, so a common range is safe
// and keeps the port bookkeeping in one place.
func StartPortEVMGateway() int {
	return integration.EVMFungible.StartPortForNode()
}

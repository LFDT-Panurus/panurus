/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlogcsp

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd Fungible (DLOG, CSP range proofs) Suite")
}

func StartPortDlog() int {
	return integration.ZKATDLogFungibleCSP.StartPortForNode()
}

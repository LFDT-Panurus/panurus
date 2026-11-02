/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlogx

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd Fungible (DLOG) Suite")
}

var _ = AfterSuite(token2.CleanupBuildArtifacts)

func StartPortDlog() int {
	return integration.ZKATDLogFungible.StartPortForNode()
}

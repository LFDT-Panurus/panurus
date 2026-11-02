/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabtoken

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/LFDT-Panurus/panurus/integration"
	token2 "github.com/LFDT-Panurus/panurus/integration/token"
)

func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd DVP (FabToken) Suite")
}

var _ = AfterSuite(token2.CleanupBuildArtifacts)

func StartPort() int {
	return integration.FabTokenDVP.StartPortForNode()
}

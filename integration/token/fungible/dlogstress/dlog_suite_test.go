/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dlog

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/integration"
	"github.com/LFDT-Panurus/panurus/integration/token"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "EndToEnd Fungible (DLOG) Stress Suite")
}

var _ = AfterSuite(token.CleanupBuildArtifacts)

func StartPortDlog() int {
	return integration.ZKATDLogFungibleStress.StartPortForNode()
}

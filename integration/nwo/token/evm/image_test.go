/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveBesuImage is the regression test for issue #2412 item 10: BESU_IMAGE used to reach only
// the `docker pull` make target, never the suite itself, which always booted DefaultBesuImage
// regardless of what was pulled. resolveBesuImage must prefer an explicitly configured image, then the
// BESU_IMAGE environment variable, and only then the hardcoded default.
func TestResolveBesuImage(t *testing.T) {
	t.Run("configured image wins over everything", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "from-env:latest")
		assert.Equal(t, "configured:latest", resolveBesuImage("configured:latest"))
	})

	t.Run("falls back to BESU_IMAGE when nothing is configured", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "from-env:latest")
		assert.Equal(t, "from-env:latest", resolveBesuImage(""))
	})

	t.Run("falls back to the hardcoded default when neither is set", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "")
		assert.Equal(t, DefaultBesuImage, resolveBesuImage(""))
	})
}

// TestResolveGatewayImage mirrors TestResolveBesuImage for the fabric-x-evm gateway backend and
// FABRICX_EVM_IMAGE.
func TestResolveGatewayImage(t *testing.T) {
	t.Run("configured image wins over everything", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "from-env:latest")
		assert.Equal(t, "configured:latest", resolveGatewayImage("configured:latest"))
	})

	t.Run("falls back to FABRICX_EVM_IMAGE when nothing is configured", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "from-env:latest")
		assert.Equal(t, "from-env:latest", resolveGatewayImage(""))
	})

	t.Run("falls back to the hardcoded default when neither is set", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "")
		assert.Equal(t, defaultGatewayImage, resolveGatewayImage(""))
	})
}

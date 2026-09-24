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
// regardless of what was pulled. resolveImage must prefer the BESU_IMAGE environment variable (the image
// make pulled), then a configured image, and only then the hardcoded default.
func TestResolveBesuImage(t *testing.T) {
	t.Run("the environment wins over a configured image", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "from-env:latest")
		assert.Equal(t, "from-env:latest", resolveImage("configured:latest", besuImageEnvVar, DefaultBesuImage))
	})

	t.Run("a configured image is used when the environment is unset", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "")
		assert.Equal(t, "configured:latest", resolveImage("configured:latest", besuImageEnvVar, DefaultBesuImage))
	})

	t.Run("falls back to BESU_IMAGE when nothing is configured", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "from-env:latest")
		assert.Equal(t, "from-env:latest", resolveImage("", besuImageEnvVar, DefaultBesuImage))
	})

	t.Run("falls back to the hardcoded default when neither is set", func(t *testing.T) {
		t.Setenv(besuImageEnvVar, "")
		assert.Equal(t, DefaultBesuImage, resolveImage("", besuImageEnvVar, DefaultBesuImage))
	})
}

// TestResolveGatewayImage mirrors TestResolveBesuImage for the fabric-x-evm gateway backend and
// FABRICX_EVM_IMAGE.
func TestResolveGatewayImage(t *testing.T) {
	t.Run("the environment wins over a configured image", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "from-env:latest")
		assert.Equal(t, "from-env:latest", resolveImage("configured:latest", fabricxEVMImageEnvVar, defaultGatewayImage))
	})

	t.Run("a configured image is used when the environment is unset", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "")
		assert.Equal(t, "configured:latest", resolveImage("configured:latest", fabricxEVMImageEnvVar, defaultGatewayImage))
	})

	t.Run("falls back to FABRICX_EVM_IMAGE when nothing is configured", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "from-env:latest")
		assert.Equal(t, "from-env:latest", resolveImage("", fabricxEVMImageEnvVar, defaultGatewayImage))
	})

	t.Run("falls back to the hardcoded default when neither is set", func(t *testing.T) {
		t.Setenv(fabricxEVMImageEnvVar, "")
		assert.Equal(t, defaultGatewayImage, resolveImage("", fabricxEVMImageEnvVar, defaultGatewayImage))
	})
}

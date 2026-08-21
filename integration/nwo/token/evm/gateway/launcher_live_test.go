/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLaunchLive boots, reaches, and stops a real EVM node container; skipped unless EVM_LAUNCHER_LIVE_IMAGE is set.
func TestLaunchLive(t *testing.T) {
	image := os.Getenv("EVM_LAUNCHER_LIVE_IMAGE")
	if image == "" {
		t.Skip("set EVM_LAUNCHER_LIVE_IMAGE to run the live launcher check")
	}

	var chainID uint64
	if s := os.Getenv("EVM_LAUNCHER_LIVE_CHAINID"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		require.NoError(t, err, "EVM_LAUNCHER_LIVE_CHAINID must be a base-10 uint64")
		chainID = v
	}

	var cmd []string
	if s := os.Getenv("EVM_LAUNCHER_LIVE_CMD"); s != "" {
		cmd = strings.Fields(s)
	}

	spec := ContainerSpec{
		Image:         image,
		Entrypoint:    os.Getenv("EVM_LAUNCHER_LIVE_ENTRYPOINT"),
		Cmd:           cmd,
		HostPort:      0,
		ContainerPort: 8545,
	}

	cfg := Config{
		ChainID:      chainID,
		StartTimeout: 60 * time.Second,
		PollInterval: 500 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	node, err := Launch(ctx, spec, cfg)
	require.NoError(t, err, "Launch should boot the EVM node container")
	require.NotNil(t, node)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = node.Stop(stopCtx)
	})

	assert.NotEmpty(t, node.Endpoint(), "Endpoint should be non-empty")
	assert.NotEmpty(t, node.ContainerID(), "ContainerID should be non-empty")

	id, err := Ping(ctx, node.Endpoint())
	require.NoError(t, err, "Ping should reach the launched node")
	if chainID != 0 {
		assert.Equal(t, chainID, id, "reported chain id should match EVM_LAUNCHER_LIVE_CHAINID")
	}
	t.Logf("live launcher: endpoint %s reachable, chain id %d", node.Endpoint(), id)

	require.NoError(t, node.Stop(ctx), "Stop should remove the container")
}

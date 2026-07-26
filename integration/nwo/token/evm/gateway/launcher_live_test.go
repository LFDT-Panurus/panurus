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

// TestLaunchLive boots a real EVM node container via the docker CLI, waits for
// its JSON-RPC endpoint to become reachable, and then stops it. It is skipped
// unless EVM_LAUNCHER_LIVE_IMAGE names a runnable EVM node image. Optional
// environment variables tune the spec:
//
//   - EVM_LAUNCHER_LIVE_IMAGE      (required) docker image reference
//   - EVM_LAUNCHER_LIVE_ENTRYPOINT (optional) --entrypoint override
//   - EVM_LAUNCHER_LIVE_CMD        (optional) command args, split on spaces
//   - EVM_LAUNCHER_LIVE_CHAINID    (optional) base-10 uint64 chain id to assert
//
// The container publishes its RPC port (8545) to an ephemeral host port. The
// node is force-stopped via t.Cleanup so the container is removed even when an
// assertion fails mid-test.
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

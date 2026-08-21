/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartTestnodeLive boots, reaches, and stops a real fabric-x-evm testnode; skipped unless EVM_TESTNODE_LIVE_IMAGE is set.
func TestStartTestnodeLive(t *testing.T) {
	image := os.Getenv("EVM_TESTNODE_LIVE_IMAGE")
	if image == "" {
		t.Skip("set EVM_TESTNODE_LIVE_IMAGE to run the live testnode check")
	}

	var chainID uint64
	if s := os.Getenv("EVM_TESTNODE_LIVE_CHAINID"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		require.NoError(t, err, "EVM_TESTNODE_LIVE_CHAINID must be a base-10 uint64")
		chainID = v
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	node, err := StartTestnode(ctx, TestnodeOptions{
		Image:        image,
		ChainID:      chainID,
		StartTimeout: 60 * time.Second,
		PollInterval: 500 * time.Millisecond,
	})
	require.NoError(t, err, "StartTestnode should boot the fabric-x-evm testnode")
	require.NotNil(t, node)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = node.Stop(stopCtx)
	})

	assert.NotEmpty(t, node.Endpoint(), "Endpoint should be non-empty")

	id, err := Ping(ctx, node.Endpoint())
	require.NoError(t, err, "Ping should reach the launched testnode")
	if chainID != 0 {
		assert.Equal(t, chainID, id, "reported chain id should match EVM_TESTNODE_LIVE_CHAINID")
	}
	t.Logf("live testnode: endpoint %s reachable, chain id %d", node.Endpoint(), id)

	require.NoError(t, node.Stop(ctx), "Stop should remove the container")
}

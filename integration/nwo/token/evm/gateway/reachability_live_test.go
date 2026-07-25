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

// TestReachabilityLive exercises Ping and WaitReachable against a real EVM node
// instead of an in-memory server. It is skipped unless EVM_LIVE_ENDPOINT points
// at a reachable JSON-RPC endpoint (e.g. a local anvil or the fabric-x-evm
// gateway devnet at http://localhost:8545). Set EVM_LIVE_CHAINID to also assert
// the reported chain id. Mirrors the driver's anvil reachability spike: present
// as a permanent check, no-op when no node is available.
func TestReachabilityLive(t *testing.T) {
	endpoint := os.Getenv("EVM_LIVE_ENDPOINT")
	if endpoint == "" {
		t.Skip("set EVM_LIVE_ENDPOINT to run the live reachability check")
	}

	var expected uint64
	if s := os.Getenv("EVM_LIVE_CHAINID"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		require.NoError(t, err, "EVM_LIVE_CHAINID must be a base-10 uint64")
		expected = v
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := WaitReachable(ctx, endpoint, expected, Config{
		StartTimeout: 10 * time.Second,
		PollInterval: 200 * time.Millisecond,
	})
	require.NoError(t, err, "endpoint %s should become reachable", endpoint)

	id, err := Ping(ctx, endpoint)
	require.NoError(t, err)
	if expected != 0 {
		assert.Equal(t, expected, id, "reported chain id should match EVM_LIVE_CHAINID")
	}
	t.Logf("live endpoint %s reachable, chain id %d", endpoint, id)
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package client_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAnvilReachability is the Phase 1.2 reachability spike. It starts a local anvil node and
// confirms the JSON-RPC surface responds to eth_chainId. It skips when anvil is not on PATH, so it
// is safe in CI; run it locally with anvil (Foundry) installed to exercise the smoke path. The same
// raw JSON-RPC probe works against a fabric-x-evm gateway by pointing endpoint at it.
func TestAnvilReachability(t *testing.T) {
	anvilBin, err := exec.LookPath("anvil")
	if err != nil {
		t.Skip("anvil not on PATH; skipping reachability spike (install Foundry to run it)")
	}

	const port = 8547
	cmd := exec.Command(anvilBin, "--port", fmt.Sprintf("%d", port), "--silent")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	chainID := pollChainID(t, endpoint, 5*time.Second)
	require.NotEmpty(t, chainID, "expected a chain id from eth_chainId")
	t.Logf("anvil reachable at %s, chainId=%s", endpoint, chainID)
}

// pollChainID calls eth_chainId until it succeeds or the timeout elapses, returning the hex chain id.
func pollChainID(t *testing.T, endpoint string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)
	for time.Now().Before(deadline) {
		resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
		if err != nil {
			time.Sleep(50 * time.Millisecond)

			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		var out struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(raw, &out) == nil && out.Result != "" {
			return out.Result
		}
		time.Sleep(50 * time.Millisecond)
	}

	return ""
}

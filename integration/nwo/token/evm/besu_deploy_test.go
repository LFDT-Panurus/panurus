/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evmabi "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/abi"
	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	evmnwo "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/nwo"
)

// These tests stand up a real Besu and deploy the real contracts into it. They are the Week-6
// building block: everything the suite does later assumes a node exists with the contracts on it.
//
// They are skipped when docker or forge are unavailable so the ordinary unit run stays hermetic, and
// they carry their own network and container names so a leftover from a previous run cannot make them
// pass or fail spuriously.

func requireBesuTooling(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed; skipping the Besu deployment test")
	}
	if _, err := docker.GetInstance(); err != nil {
		t.Skip("docker not available; skipping the Besu deployment test")
	}
}

// startTestBesu boots a node on its own docker network and cleans both up afterwards.
func startTestBesu(t *testing.T, name string, port int) *Besu {
	t.Helper()
	requireBesuTooling(t)

	d, err := docker.GetInstance()
	require.NoError(t, err)

	networkID := name + "-net"
	// A leftover network from an interrupted run would otherwise fail the create.
	_ = exec.Command("docker", "network", "rm", networkID).Run()
	require.NoError(t, d.CreateNetwork(networkID))
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", networkID).Run() })

	_ = exec.Command("docker", "rm", "-f", name).Run()
	node, err := StartBesu(context.Background(), BesuConfig{
		NetworkID: networkID, Name: name, Port: port, StartTimeout: 3 * time.Minute,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	return node
}

// TestDeployOntoBesu is the C3 gate: the driver's own contracts, deployed by the project's own forge
// script, onto a real Besu, with the deployed state readable afterwards.
func TestDeployOntoBesu(t *testing.T) {
	node := startTestBesu(t, "evm-deploy-besu", 18745)

	endorser, err := GenerateIdentity(t.TempDir(), "endorser-1")
	require.NoError(t, err)
	require.NotEqual(t, evmclient.Address{}, endorser.Address, "a generated endorser must have an address")

	backend := &ForgeBackend{Endpoint: node.Endpoint()}
	pp0 := []byte("besu-public-params-v0")

	deployment, err := backend.Deploy(evmnwo.DeploySpec{
		TMS:          evmnwo.TMSRef{Network: "evm", Namespace: "token"},
		Endorsers:    []evmclient.Address{endorser.Address},
		Threshold:    1,
		PublicParams: pp0,
	})
	require.NoError(t, err)
	require.NotEqual(t, evmclient.Address{}, deployment.TokenState, "the deployment must report a token state")
	t.Logf("deployed on besu: tokenState=%s verifier=%s", deployment.TokenState, deployment.EndorsementVerifier)

	// The contracts must actually be live on the node: read the seeded public parameters back through
	// the driver's own client, which is what the endorsers will do.
	c, err := evmclient.NewJSONRPCClient(node.Endpoint(), nil)
	require.NoError(t, err)

	raw, err := c.Call(context.Background(), deployment.TokenState,
		evmabi.MethodID("getPublicParameters()"), evmclient.BlockTagLatest)
	require.NoError(t, err)
	stored, err := evmabi.DecodeBytes(raw)
	require.NoError(t, err)
	assert.Equal(t, pp0, stored, "the chain must return the parameters the deployment seeded")

	// And the version starts at zero, which is what an endorser asserts a delta against.
	raw, err = c.Call(context.Background(), deployment.TokenState,
		evmabi.MethodID("getPublicParamsVersion()"), evmclient.BlockTagLatest)
	require.NoError(t, err)
	version, err := evmabi.DecodeUint64(raw)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), version)
}

// TestDeployRejectsBadSpec checks the guards that would otherwise produce a contract nobody can use.
func TestDeployRejectsBadSpec(t *testing.T) {
	backend := &ForgeBackend{Endpoint: "http://127.0.0.1:1"}

	t.Run("no endorsers", func(t *testing.T) {
		_, err := backend.Deploy(evmnwo.DeploySpec{Threshold: 1})
		require.Error(t, err)
	})

	t.Run("threshold above the set size", func(t *testing.T) {
		_, err := backend.Deploy(evmnwo.DeploySpec{
			Endorsers: []evmclient.Address{{}}, Threshold: 2,
		})
		require.Error(t, err)
	})

	t.Run("zero threshold", func(t *testing.T) {
		_, err := backend.Deploy(evmnwo.DeploySpec{Endorsers: []evmclient.Address{{}}})
		require.Error(t, err)
	})
}

func TestGenerateIdentityIsUniqueAndLoadable(t *testing.T) {
	dir := t.TempDir()
	first, err := GenerateIdentity(dir, "a")
	require.NoError(t, err)
	second, err := GenerateIdentity(dir, "b")
	require.NoError(t, err)

	assert.NotEqual(t, first.Address, second.Address, "each endorser must get its own identity")
	assert.NotEqual(t, first.Keystore, second.Keystore)
}

func TestWriteFundedSubmitter(t *testing.T) {
	id, err := WriteFundedSubmitter(t.TempDir(), "submitter")
	require.NoError(t, err)
	assert.Equal(t, DevFundedAddress, id.Address.Hex(),
		"the submitter must be a pre-funded account, since it pays for gas")
}

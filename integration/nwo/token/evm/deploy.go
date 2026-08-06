/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	evmclient "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	evmnwo "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/nwo"
)

// contractsDirFromSource locates the driver's forge project relative to this source file rather than
// to the working directory, which differs between a go test run, the integration suite, and a make
// target. Walking up from the source keeps it correct in all of them.
func contractsDirFromSource() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}

	// this file: <root>/integration/nwo/token/evm/deploy.go
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))

	return filepath.Join(root, "x", "token", "services", "network", "evm", "contracts")
}

// ForgeBackend deploys the driver's contracts with the project's own forge script, so the test
// network stands a TMS up exactly the way an operator would rather than through a parallel path that
// could drift from it.
//
// It satisfies the driver-owned Backend, which is the seam the Besu and fabricx+EVM backends share.
type ForgeBackend struct {
	// Endpoint is the node's JSON-RPC URL.
	Endpoint string
	// ContractsDir is the forge project root; DefaultContractsDir when empty.
	ContractsDir string
	// DeployerKey is the hex private key that pays for the deployment. It must be funded, which on a
	// development network means one of the pre-funded accounts.
	DeployerKey string
}

// Compile-time check that the backend satisfies the driver-owned contract.
var _ evmnwo.Backend = (*ForgeBackend)(nil)

// Deploy stands up the EndorsementVerifier and a per-TMS TokenState clone, seeding the endorser set,
// threshold, graph-hiding mode and the initial public parameters.
func (b *ForgeBackend) Deploy(spec evmnwo.DeploySpec) (evmnwo.Deployment, error) {
	if len(spec.Endorsers) == 0 {
		return evmnwo.Deployment{}, errors.New("evm nwo: cannot deploy without an endorser set")
	}
	if spec.Threshold == 0 || int(spec.Threshold) > len(spec.Endorsers) {
		return evmnwo.Deployment{}, errors.Errorf(
			"evm nwo: threshold %d out of range for %d endorsers", spec.Threshold, len(spec.Endorsers))
	}

	addresses := make([]string, len(spec.Endorsers))
	for i, e := range spec.Endorsers {
		addresses[i] = e.Hex()
	}

	out, err := b.runForge(
		"EVM_ENDORSERS="+strings.Join(addresses, ","),
		"EVM_THRESHOLD="+strconv.FormatUint(uint64(spec.Threshold), 10),
		"EVM_PP0=0x"+hex.EncodeToString(spec.PublicParams),
		"EVM_GRAPH_HIDING="+strconv.FormatBool(spec.GraphHiding),
	)
	if err != nil {
		return evmnwo.Deployment{}, err
	}

	return parseDeployment(out)
}

// UpdatePublicParams seeds or updates the public parameters for a TMS.
//
// Not implemented yet: after bootstrap the parameters are owned by the endorser quorum, so an update
// is an endorsed setup delta rather than a deploy-time action. Seeding at version 0 happens through
// Deploy. The endorsed update flow lands with the public-parameters work.
func (b *ForgeBackend) UpdatePublicParams(evmnwo.TMSRef, []byte) error {
	return errors.New("evm nwo: public parameters are updated through an endorsed setup delta, not the deployer")
}

// runForge executes the deploy script against the node and returns its output. The private key is
// passed as a flag rather than an environment variable because the script's broadcast takes its
// sender from the command line.
func (b *ForgeBackend) runForge(env ...string) (string, error) {
	dir := b.ContractsDir
	if dir == "" {
		dir = contractsDirFromSource()
	}
	if dir == "" {
		return "", errors.New("evm nwo: could not locate the contracts directory")
	}
	key := b.DeployerKey
	if key == "" {
		key = DevFundedKey
	}

	cmd := exec.CommandContext(context.Background(), "forge", "script", "script/Deploy.s.sol:Deploy",
		"--rpc-url", b.Endpoint, "--broadcast", "--json",
		"--private-key", "0x"+strings.TrimPrefix(key, "0x"))
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), env...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrapf(err, "evm nwo: forge deploy failed: %s", out)
	}

	return string(out), nil
}

// parseDeployment pulls the deployed addresses out of the script's JSON output.
func parseDeployment(out string) (evmnwo.Deployment, error) {
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var payload struct {
			Returns struct {
				TokenState struct {
					Value string `json:"value"`
				} `json:"tokenState"`
				Verifier struct {
					Value string `json:"value"`
				} `json:"verifier"`
			} `json:"returns"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			continue
		}
		if payload.Returns.TokenState.Value == "" {
			continue
		}

		tokenState, err := evmclient.HexToAddress(payload.Returns.TokenState.Value)
		if err != nil {
			return evmnwo.Deployment{}, errors.Wrap(err, "evm nwo: the deployed token state address is malformed")
		}
		deployment := evmnwo.Deployment{TokenState: tokenState}

		// The verifier is reported by the same script; it is not fatal if a future script stops
		// returning it, since the TokenState holds the authoritative reference.
		if v := payload.Returns.Verifier.Value; v != "" {
			verifier, err := evmclient.HexToAddress(v)
			if err != nil {
				return evmnwo.Deployment{}, errors.Wrap(err, "evm nwo: the deployed verifier address is malformed")
			}
			deployment.EndorsementVerifier = verifier
		}

		return deployment, nil
	}

	return evmnwo.Deployment{}, errors.Errorf("evm nwo: could not find the deployed addresses in the forge output:\n%s", out)
}

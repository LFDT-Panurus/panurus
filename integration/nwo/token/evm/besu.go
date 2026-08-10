/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common/docker"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	dcli "github.com/moby/moby/client"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
)

var logger = logging.MustGetLogger()

// DefaultBesuImage is the node image the test network boots. Besu is the acceptance backend for the
// driver, so the integration suite runs against it rather than against a lighter development chain:
// the point of the suite is that the driver works on the real thing.
const DefaultBesuImage = "hyperledger/besu:24.3.0"

// DefaultChainID is the chain id the dev network runs with. It matches the value the deploy scripts
// and the driver configuration use.
const DefaultChainID int64 = 1337

// BesuConfig describes the node to boot.
type BesuConfig struct {
	// Image is the container image; DefaultBesuImage when empty.
	Image string
	// Name is the container name.
	Name string
	// Port is the host port the JSON-RPC endpoint is published on.
	Port int
	// ChainID is the chain the node runs; DefaultChainID when zero.
	ChainID int64
	// StartTimeout bounds how long to wait for the node to answer JSON-RPC.
	StartTimeout time.Duration
}

// Besu is a running Besu node.
type Besu struct {
	cfg         BesuConfig
	containerID string
	endpoint    string
}

// Endpoint returns the node's JSON-RPC URL as reachable from the host.
func (b *Besu) Endpoint() string { return b.endpoint }

// ChainID returns the chain the node is running.
func (b *Besu) ChainID() int64 { return b.cfg.ChainID }

// StartBesu boots a Besu node in development mode and waits until it answers JSON-RPC.
//
// Development mode is deliberate: it mines instantly and pre-funds well-known accounts, which is what
// a test network wants. It is a real Besu either way, so the driver exercises the same client
// behaviour it will see in a deployment; only the consensus and funding are shortcut.
func StartBesu(ctx context.Context, cfg BesuConfig) (*Besu, error) {
	if cfg.Image == "" {
		cfg.Image = DefaultBesuImage
	}
	if cfg.ChainID == 0 {
		cfg.ChainID = DefaultChainID
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 2 * time.Minute
	}
	if cfg.Port == 0 {
		return nil, errors.New("evm nwo: no host port assigned for besu")
	}

	d, err := docker.GetInstance()
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: docker is not available")
	}
	if err := d.CheckImagesExist(cfg.Image); err != nil {
		return nil, errors.Wrapf(err, "evm nwo: the besu image [%s] is missing; pull it first", cfg.Image)
	}

	cli, err := dcli.New(dcli.FromEnv)
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to create a docker client")
	}

	// A container left behind by an interrupted run would otherwise block this one on a name clash.
	// The test network is generated fresh each time, so removing it is the same intent as
	// DeleteOnStart rather than a destructive surprise.
	if _, err := cli.ContainerRemove(ctx, cfg.Name, dcli.ContainerRemoveOptions{Force: true}); err != nil {
		logger.Debugf("no previous besu container to remove: %v", err)
	}

	// On Linux the container needs an explicit route back to the host for anything the node has to
	// reach; the fabric platforms add the same host alias.
	var extraHosts []string
	if runtime.GOOS == "linux" {
		extraHosts = append(extraHosts, "host.docker.internal:host-gateway")
	}

	const rpcPort = 8545
	resp, err := cli.ContainerCreate(ctx, dcli.ContainerCreateOptions{
		Name: cfg.Name,
		Config: &container.Config{
			Image:        cfg.Image,
			Tty:          true,
			AttachStdout: true,
			AttachStderr: true,
			ExposedPorts: docker.PortSet(rpcPort),
			Cmd:          besuArgs(cfg),
		},
		HostConfig: &container.HostConfig{
			ExtraHosts: extraHosts,
			// The helper maps a port to itself, but besu listens on 8545 inside the container and is
			// published on an allocated host port, so the mapping is built explicitly.
			PortBindings: network.PortMap{
				network.MustParsePort(strconv.Itoa(rpcPort) + "/tcp"): []network.PortBinding{{
					HostIP:   netip.MustParseAddr("0.0.0.0"),
					HostPort: strconv.Itoa(cfg.Port),
				}},
			},
		},
		// No NetworkingConfig: the container joins docker's default bridge. Everything that talks to
		// this node does so over the published port on 127.0.0.1 (the FSC nodes are host processes and
		// forge runs on the host), so a dedicated network would be created, joined, and never used.
	})
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to create the besu container")
	}

	if _, err := cli.ContainerStart(ctx, resp.ID, dcli.ContainerStartOptions{}); err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to start the besu container")
	}

	if err := docker.StartLogs(cli, resp.ID, "besu."+resp.ID[:8]); err != nil {
		// Logs are diagnostics; losing them must not fail the network.
		logger.Warnf("could not stream besu logs: %v", err)
	}

	node := &Besu{
		cfg:         cfg,
		containerID: resp.ID,
		endpoint:    "http://127.0.0.1:" + strconv.Itoa(cfg.Port),
	}
	if err := node.waitReady(ctx); err != nil {
		return nil, err
	}
	logger.Infof("besu is up at %s (chain %d)", node.endpoint, cfg.ChainID)

	return node, nil
}

// Stop removes the container. It is safe to call on a partially started node.
func (b *Besu) Stop(ctx context.Context) error {
	if b == nil || b.containerID == "" {
		return nil
	}
	cli, err := dcli.New(dcli.FromEnv)
	if err != nil {
		return errors.Wrap(err, "evm nwo: failed to create a docker client")
	}

	if _, err := cli.ContainerRemove(ctx, b.containerID, dcli.ContainerRemoveOptions{Force: true}); err != nil {
		return errors.Wrap(err, "evm nwo: failed to remove the besu container")
	}

	return nil
}

// besuArgs builds the development-mode command line: instant mining, JSON-RPC open to the container
// network, and the APIs the driver calls.
func besuArgs(cfg BesuConfig) []string {
	return []string{
		"--network=dev",
		"--miner-enabled",
		"--miner-coinbase=0xf17f52151EbEF6C7334FAD080c5704D77216b732",
		"--rpc-http-enabled",
		"--rpc-http-host=0.0.0.0",
		"--rpc-http-port=8545",
		"--rpc-http-api=ETH,NET,WEB3,TXPOOL",
		"--rpc-http-cors-origins=all",
		"--host-allowlist=*",
		"--min-gas-price=0",
		fmt.Sprintf("--network-id=%d", cfg.ChainID),
	}
}

// waitReady polls eth_chainId until the node answers, so callers get a node that is actually usable
// rather than one whose container merely started.
func (b *Besu) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(b.cfg.StartTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(b.cfg.Port)), time.Second); err == nil {
			_ = conn.Close()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, body)
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				if resp, err := client.Do(req); err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						return nil
					}
				}
			}
			body = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)
		}
		time.Sleep(500 * time.Millisecond)
	}

	return errors.Errorf("evm nwo: besu did not become ready at %s within %s", b.endpoint, b.cfg.StartTimeout)
}

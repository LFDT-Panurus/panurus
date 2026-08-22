/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"context"
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
)

// defaultGatewayImage is the published fabric-x-evm image booted when none is configured.
const defaultGatewayImage = "ghcr.io/hyperledger/fabric-x-evm:0.1.3"

// defaultGatewayChainID is the chain id the gateway runs when none is configured; it mirrors the testnode default.
const defaultGatewayChainID int64 = 31337

// gatewayStartTimeout bounds how long to wait for the gateway to answer JSON-RPC.
const gatewayStartTimeout = 2 * time.Minute

// Gateway is a running fabric-x-evm gateway testnode.
type Gateway struct {
	cfg         gatewayConfig
	containerID string
	endpoint    string
}

// gatewayConfig describes the gateway node to boot.
type gatewayConfig struct {
	// Image is the container image.
	Image string
	// Name is the container name.
	Name string
	// Port is the host port the JSON-RPC endpoint is published on.
	Port int
	// ChainID is the chain the node runs.
	ChainID int64
	// StartTimeout bounds how long to wait for the node to answer JSON-RPC.
	StartTimeout time.Duration
}

// Gateway is a Node.
var _ Node = (*Gateway)(nil)

// Endpoint returns the node's JSON-RPC URL as reachable from the host.
func (g *Gateway) Endpoint() string { return g.endpoint }

// ChainID returns the chain the node is running.
func (g *Gateway) ChainID() int64 { return g.cfg.ChainID }

// startGatewayNode boots a fabric-x-evm gateway testnode on the given host port and waits until it
// answers JSON-RPC, returning it as a Node.
//
// The testnode is the self-contained fabric-x-evm chain: it runs a gasless gateway that mines
// instantly, which is what a test network wants. The driver exercises the same client behaviour it
// will see in a deployment; only the consensus and gas policy are shortcut.
func startGatewayNode(ctx context.Context, image string, chainID int64, port int) (Node, error) {
	cfg := gatewayConfig{
		Image:        image,
		Name:         "fabricx-evm-" + strconv.Itoa(port),
		Port:         port,
		ChainID:      chainID,
		StartTimeout: gatewayStartTimeout,
	}
	if cfg.Image == "" {
		cfg.Image = defaultGatewayImage
	}
	if cfg.ChainID <= 0 {
		cfg.ChainID = defaultGatewayChainID
	}
	if cfg.Port == 0 {
		return nil, errors.New("evm nwo: no host port assigned for the fabric-x-evm gateway")
	}

	d, err := docker.GetInstance()
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: docker is not available")
	}
	if err := d.CheckImagesExist(cfg.Image); err != nil {
		return nil, errors.Wrapf(err, "evm nwo: the fabric-x-evm image [%s] is missing; pull it first", cfg.Image)
	}

	cli, err := dcli.New(dcli.FromEnv)
	if err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to create a docker client")
	}

	// A container left behind by an interrupted run would otherwise block this one on a name clash.
	// The test network is generated fresh each time, so removing it is the same intent as
	// DeleteOnStart rather than a destructive surprise.
	if _, err := cli.ContainerRemove(ctx, cfg.Name, dcli.ContainerRemoveOptions{Force: true}); err != nil {
		logger.Debugf("no previous fabric-x-evm container to remove: %v", err)
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
			Cmd:          gatewayArgs(cfg),
		},
		HostConfig: &container.HostConfig{
			ExtraHosts: extraHosts,
			// The helper maps a port to itself, but the testnode listens on 8545 inside the container and
			// is published on an allocated host port, so the mapping is built explicitly.
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
		return nil, errors.Wrap(err, "evm nwo: failed to create the fabric-x-evm container")
	}

	if _, err := cli.ContainerStart(ctx, resp.ID, dcli.ContainerStartOptions{}); err != nil {
		return nil, errors.Wrap(err, "evm nwo: failed to start the fabric-x-evm container")
	}

	if err := docker.StartLogs(cli, resp.ID, "fabricx-evm."+resp.ID[:8]); err != nil {
		// Logs are diagnostics; losing them must not fail the network.
		logger.Warnf("could not stream fabric-x-evm logs: %v", err)
	}

	node := &Gateway{
		cfg:         cfg,
		containerID: resp.ID,
		endpoint:    "http://127.0.0.1:" + strconv.Itoa(cfg.Port),
	}
	if err := node.waitReady(ctx); err != nil {
		return nil, err
	}
	logger.Infof("fabric-x-evm gateway is up at %s (chain %d)", node.endpoint, cfg.ChainID)

	return node, nil
}

// Stop removes the container. It is safe to call on a partially started node.
func (g *Gateway) Stop(ctx context.Context) error {
	if g == nil || g.containerID == "" {
		return nil
	}
	cli, err := dcli.New(dcli.FromEnv)
	if err != nil {
		return errors.Wrap(err, "evm nwo: failed to create a docker client")
	}

	if _, err := cli.ContainerRemove(ctx, g.containerID, dcli.ContainerRemoveOptions{Force: true}); err != nil {
		return errors.Wrap(err, "evm nwo: failed to remove the fabric-x-evm container")
	}

	return nil
}

// gatewayArgs builds the fabric-x-evm testnode command line: JSON-RPC open to the container network on
// 8545, running the configured chain id.
func gatewayArgs(cfg gatewayConfig) []string {
	return []string{
		"testnode",
		"--listen", "0.0.0.0:8545",
		"--chain-id", strconv.FormatInt(cfg.ChainID, 10),
	}
}

// waitReady polls eth_chainId until the node answers, so callers get a node that is actually usable
// rather than one whose container merely started.
func (g *Gateway) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(g.cfg.StartTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(g.cfg.Port)), time.Second); err == nil {
			_ = conn.Close()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, body)
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

	return errors.Errorf("evm nwo: fabric-x-evm gateway did not become ready at %s within %s", g.endpoint, g.cfg.StartTimeout)
}

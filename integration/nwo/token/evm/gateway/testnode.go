/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"context"
	"strconv"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// defaultTestnodeChainID is the EVM chain id used by the fabric-x-evm
	// self-contained testnode when TestnodeOptions.ChainID is unset (0).
	defaultTestnodeChainID = 31337
	// defaultTestnodeListenPort is the in-container JSON-RPC listen port used by
	// the testnode when TestnodeOptions.ListenPort is unset (0). It matches the
	// testnode's own default --listen port.
	defaultTestnodeListenPort = 8545
)

// TestnodeOptions configures the fabric-x-evm self-contained testnode launched
// by StartTestnode. The testnode runs the image's "testnode" subcommand, which
// serves Ethereum JSON-RPC on the in-container listen port.
type TestnodeOptions struct {
	// Image is the fabric-x-evm docker image reference that bundles the
	// self-contained testnode. It is required; StartTestnode returns an error
	// when it is empty. There is no sensible default image, since the image is
	// built from fabric-x-evm main and published to the caller's own registry.
	Image string
	// ChainID is the EVM chain id passed to the testnode via "--chain-id". When
	// 0, it defaults to 31337.
	ChainID uint64
	// HostPort is the host port to publish the JSON-RPC endpoint on. When 0, an
	// ephemeral host port is published and discovered by Launch.
	HostPort int
	// ListenPort is the in-container JSON-RPC listen port passed to the testnode
	// via "--listen 0.0.0.0:<ListenPort>". When 0, it defaults to 8545.
	ListenPort int
	// StartTimeout is how long StartTestnode waits for the JSON-RPC endpoint to
	// become reachable before giving up. When 0, Config.WithDefaults applies a
	// 60s default.
	StartTimeout time.Duration
	// PollInterval is the delay between successive reachability probes. When 0,
	// Config.WithDefaults applies a 500ms default.
	PollInterval time.Duration
}

// withDefaults returns a copy of the options with ChainID and ListenPort filled
// in with their default values when unset (0). Timeout fields are left as-is;
// Config.WithDefaults supplies their defaults during the reachability wait.
func (o TestnodeOptions) withDefaults() TestnodeOptions {
	if o.ChainID == 0 {
		o.ChainID = defaultTestnodeChainID
	}
	if o.ListenPort == 0 {
		o.ListenPort = defaultTestnodeListenPort
	}

	return o
}

// buildTestnodeSpec builds the ContainerSpec for the fabric-x-evm testnode from
// o in a deterministic way so it can be unit-tested. It applies o.withDefaults()
// internally, so callers may pass zero-valued ChainID/ListenPort. The image's
// ENTRYPOINT is "fxevm", so "testnode ..." is passed as the container command
// with no entrypoint override, and no env or mounts are needed.
func buildTestnodeSpec(o TestnodeOptions) ContainerSpec {
	o = o.withDefaults()

	return ContainerSpec{
		Image: o.Image,
		Cmd: []string{
			"testnode",
			"--listen", "0.0.0.0:" + strconv.Itoa(o.ListenPort),
			"--chain-id", strconv.FormatUint(o.ChainID, 10),
		},
		HostPort:      o.HostPort,
		ContainerPort: o.ListenPort,
	}
}

// StartTestnode launches a fabric-x-evm self-contained testnode container and
// waits for its JSON-RPC endpoint to become reachable, returning the running
// Node. It requires o.Image to be non-empty and defaults ChainID to 31337 and
// ListenPort to 8545 when unset. Readiness is confirmed by an eth_chainId probe;
// when ChainID is non-zero the probe also asserts the reported chain id matches.
func StartTestnode(ctx context.Context, o TestnodeOptions) (*Node, error) {
	if o.Image == "" {
		return nil, errors.New("evm testnode: image must not be empty")
	}

	o = o.withDefaults()
	spec := buildTestnodeSpec(o)
	cfg := Config{
		ChainID:      o.ChainID,
		StartTimeout: o.StartTimeout,
		PollInterval: o.PollInterval,
	}

	node, err := Launch(ctx, spec, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed starting fabric-x-evm testnode")
	}

	return node, nil
}

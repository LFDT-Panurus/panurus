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
	// defaultTestnodeChainID is the chain id used when TestnodeOptions.ChainID is unset.
	defaultTestnodeChainID = 31337
	// defaultTestnodeListenPort is the in-container listen port used when TestnodeOptions.ListenPort is unset.
	defaultTestnodeListenPort = 8545
)

// TestnodeOptions configures the fabric-x-evm self-contained testnode launched by StartTestnode.
type TestnodeOptions struct {
	// Image is the fabric-x-evm docker image reference. Required.
	Image string
	// ChainID is the chain id passed via "--chain-id"; 0 defaults to 31337.
	ChainID uint64
	// HostPort is the host port to publish the endpoint on; 0 publishes an ephemeral port.
	HostPort int
	// ListenPort is the in-container listen port passed via "--listen"; 0 defaults to 8545.
	ListenPort int
	// StartTimeout is how long StartTestnode waits for reachability; 0 uses the 60s default.
	StartTimeout time.Duration
	// PollInterval is the delay between reachability probes; 0 uses the 500ms default.
	PollInterval time.Duration
}

// withDefaults returns a copy of the options with ChainID and ListenPort defaulted when unset.
func (o TestnodeOptions) withDefaults() TestnodeOptions {
	if o.ChainID == 0 {
		o.ChainID = defaultTestnodeChainID
	}
	if o.ListenPort == 0 {
		o.ListenPort = defaultTestnodeListenPort
	}

	return o
}

// buildTestnodeSpec builds the fabric-x-evm testnode ContainerSpec from o deterministically, applying withDefaults.
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

// StartTestnode launches a fabric-x-evm testnode container and waits for its JSON-RPC endpoint to become reachable.
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

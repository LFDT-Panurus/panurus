/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"strconv"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// defaultHost is the host used by Endpoint when none is provided.
	defaultHost = "127.0.0.1"
	// defaultStartTimeout is the reachability wait applied by WithDefaults.
	defaultStartTimeout = 60 * time.Second
	// defaultPollInterval is the probe delay applied by WithDefaults.
	defaultPollInterval = 500 * time.Millisecond
	// maxPort is the highest valid TCP port number.
	maxPort = 65535
)

// Config describes an EVM node whose JSON-RPC endpoint must be reached, with the reachability-probe timing.
type Config struct {
	// Image is the docker image reference for the EVM node.
	Image string
	// ChainID is the expected EVM chain id; 0 means any answering node is reachable.
	ChainID uint64
	// RPCPort is the host port on which the JSON-RPC endpoint is exposed.
	RPCPort int
	// StartTimeout is how long WaitReachable waits for the endpoint to answer.
	StartTimeout time.Duration
	// PollInterval is the delay between successive reachability probes.
	PollInterval time.Duration
}

// Validate reports whether the Config is well formed.
func (c Config) Validate() error {
	if c.Image == "" {
		return errors.New("evm gateway config: image must not be empty")
	}
	if c.RPCPort < 1 || c.RPCPort > maxPort {
		return errors.Errorf("evm gateway config: rpc port %d out of range 1..%d", c.RPCPort, maxPort)
	}

	return nil
}

// WithDefaults returns a copy of the Config with StartTimeout and PollInterval defaulted when unset (<= 0).
func (c Config) WithDefaults() Config {
	if c.StartTimeout <= 0 {
		c.StartTimeout = defaultStartTimeout
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}

	return c
}

// Endpoint returns the JSON-RPC URL for the node on the given host, defaulting to 127.0.0.1 when host is empty.
func (c Config) Endpoint(host string) string {
	if host == "" {
		host = defaultHost
	}

	return "http://" + host + ":" + strconv.Itoa(c.RPCPort)
}

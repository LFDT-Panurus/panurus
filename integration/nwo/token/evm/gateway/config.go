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
	// defaultStartTimeout is the reachability wait applied by WithDefaults when
	// StartTimeout is unset (<= 0).
	defaultStartTimeout = 60 * time.Second
	// defaultPollInterval is the delay between probes applied by WithDefaults
	// when PollInterval is unset (<= 0).
	defaultPollInterval = 500 * time.Millisecond
	// maxPort is the highest valid TCP port number.
	maxPort = 65535
)

// Config describes an EVM node whose JSON-RPC endpoint must be reached, along
// with the timing parameters that govern the reachability probe.
type Config struct {
	// Image is the docker image reference for the EVM node (e.g. the
	// fabric-x-evm gateway image). It is not used by the reachability probe yet
	// but documents the eventual container spec.
	Image string
	// ChainID is the expected EVM chain id. A value of 0 means the chain id is
	// not checked and any node that answers is considered reachable.
	ChainID uint64
	// RPCPort is the host port on which the JSON-RPC endpoint is exposed.
	RPCPort int
	// StartTimeout is how long WaitReachable waits for the endpoint to answer
	// before giving up.
	StartTimeout time.Duration
	// PollInterval is the delay between successive reachability probes.
	PollInterval time.Duration
}

// Validate reports whether the Config is well formed. It returns a wrapped
// error when Image is empty or RPCPort is outside the valid 1..65535 range.
// Non-positive StartTimeout and PollInterval are allowed because WithDefaults
// supplies defaults for them.
func (c Config) Validate() error {
	if c.Image == "" {
		return errors.New("evm gateway config: image must not be empty")
	}
	if c.RPCPort < 1 || c.RPCPort > maxPort {
		return errors.Errorf("evm gateway config: rpc port %d out of range 1..%d", c.RPCPort, maxPort)
	}

	return nil
}

// WithDefaults returns a copy of the Config with StartTimeout and PollInterval
// filled in with their default values when they are unset (<= 0). All other
// fields are preserved.
func (c Config) WithDefaults() Config {
	if c.StartTimeout <= 0 {
		c.StartTimeout = defaultStartTimeout
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}

	return c
}

// Endpoint returns the JSON-RPC URL for the node on the given host, e.g.
// "http://127.0.0.1:8545". When host is empty, 127.0.0.1 is used.
func (c Config) Endpoint(host string) string {
	if host == "" {
		host = defaultHost
	}

	return "http://" + host + ":" + strconv.Itoa(c.RPCPort)
}

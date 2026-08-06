/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"time"

	evmnwo "github.com/LFDT-Panurus/panurus/x/token/services/network/evm/nwo"
)

// TopologyName is the platform name an EVM-backed topology registers under. Suites select this
// backend by name, the way they select fabric or fabricx today.
const TopologyName = "evm"

// Defaults for a test network. They are tuned for a locally mined chain rather than a public one:
// blocks appear immediately, so polling fast and reading at the chain head costs nothing and keeps
// the suite quick.
const (
	DefaultBlockTag        = "latest"
	DefaultPollInterval    = 200 * time.Millisecond
	DefaultFinalityTimeout = 2 * time.Minute
)

// EndorserBinding pairs an endorser's Ethereum address with the FSC identity that speaks for it. The
// driver needs both: the address to recover a signature to, the identity to route a request to
// (design §6.1).
type EndorserBinding struct {
	Address     string
	FSCIdentity string
}

// NodeConfig is everything a single node's token extension needs about the EVM backend. It is
// assembled once the node is up and the contracts are deployed, then rendered through Extension.
type NodeConfig struct {
	// NodeName is this node's FSC identity, used as its endorser identity when it endorses.
	NodeName string
	// Endpoint is the node's JSON-RPC URL.
	Endpoint string
	// ChainID is the chain the driver signs for.
	ChainID int64

	// Deployment carries the contract addresses this TMS was deployed with.
	Deployment evmnwo.Deployment

	// Finality tuning.
	BlockTag        string
	PollInterval    time.Duration
	FinalityTimeout time.Duration
	FromBlock       uint64

	// IsEndorser marks this node as one of the endorsers, which gives it a key and an entry in the
	// endorser set.
	IsEndorser       bool
	EndorserKeystore string
	EndorserAddress  string

	// A node that never broadcasts needs no submitter key: it can still endorse and read.
	SubmitterKeystore string
	SubmitterAddress  string

	// Endorsement policy, identical across the nodes of one TMS.
	Threshold uint
	Allowlist []string
	Endorsers []EndorserBinding
}

// WithDefaults fills the tuning fields a caller left unset.
func (c NodeConfig) WithDefaults() NodeConfig {
	if c.BlockTag == "" {
		c.BlockTag = DefaultBlockTag
	}
	if c.PollInterval <= 0 {
		c.PollInterval = DefaultPollInterval
	}
	if c.FinalityTimeout <= 0 {
		c.FinalityTimeout = DefaultFinalityTimeout
	}

	return c
}

// HasSubmitter reports whether this node can broadcast.
func (c NodeConfig) HasSubmitter() bool { return c.SubmitterKeystore != "" }

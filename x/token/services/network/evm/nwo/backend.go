/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nwo

import (
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
)

// TMSRef identifies the token management system a backend operation targets. It is the evm module's
// own minimal reference (network, channel, namespace), so this package needs no panurus token types.
type TMSRef struct {
	Network   string
	Channel   string
	Namespace string
}

// DeploySpec is the input to standing up one TMS's on-chain state: the endorser set and threshold the
// EndorsementVerifier is seeded with, the driver mode (graphHiding selects the spentRefs semantics),
// and the initial public parameters (version 0). Endorsers are Ethereum addresses here; the address
// to FSC identity binding the driver's config carries is assembled by the harness alongside this.
type DeploySpec struct {
	TMS          TMSRef
	Endorsers    []client.Address
	Threshold    uint
	GraphHiding  bool
	PublicParams []byte
}

// Deployment is the result of Deploy: the contract addresses a node's evm config block points at
// (services.network.evm.contracts.*).
type Deployment struct {
	TokenState          client.Address
	EndorsementVerifier client.Address
}

// Backend stands up and maintains the EVM on-chain state a TMS needs during integration tests. It is
// the driver-owned NWO surface: a test harness deploys the contracts through Deploy and seeds or
// updates public parameters through UpdatePublicParams. Standing up the node itself (Besu vs the
// fabricx+EVM gateway) is the concrete backend's concern and is not part of this contract, so the two
// backends share this shape and differ only in how the node is provisioned underneath.
type Backend interface {
	// Deploy deploys the EndorsementVerifier and a per-TMS TokenState clone for spec.TMS, seeding the
	// endorser set, threshold, graphHiding mode and public parameters (version 0), and returns the
	// deployed addresses.
	Deploy(spec DeploySpec) (Deployment, error)

	// UpdatePublicParams seeds or updates the on-chain public parameters for the TMS, the value the
	// driver reads back via getPublicParameters / getPublicParamsVersion.
	UpdatePublicParams(tms TMSRef, ppRaw []byte) error
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package nwo is the EVM network driver's own integration-network support: the seam a test harness
// uses to stand up the driver's on-chain backend (deploy the contracts, seed and update public
// parameters) so the token integration suites can run against an EVM backend by switching topology.
//
// It lives inside the evm module, not in a consumer's integration tree, because the module is
// developed as if it lived in a different repo (design §15.6, working rule R7): its integration
// surface ships with it. That is why this package depends only on evm-module types and never on the
// panurus integration module (the dependency runs the other way, integration requires this module).
//
// The layering mirrors fabricx: the backend's own repo (FSC for fabricx, this module for evm) ships
// the platform, and a consumer's NWO framework adds a thin adapter over it. So a panurus adapter
// (integration/nwo/token/evm) implements the token-layer Backend by delegating to the Backend defined
// here; that adapter lands with the concrete backend and the go.mod require+replace (Week 6), which
// is why it is not part of this skeleton.
//
// # The contract (backend.go)
//
//   - Backend is the driver-owned surface a test harness drives: Deploy stands up a TMS's contracts
//     (EndorsementVerifier + a per-TMS TokenState clone, seeding PP v0 + endorser set + threshold +
//     graphHiding) and returns the addresses a node's evm config points at; UpdatePublicParams seeds
//     or updates the on-chain public parameters.
//   - How the node itself is stood up (Besu in dev mode vs the fabricx+EVM gateway container) is the
//     concrete backend's concern, layered on top; this package fixes only the driver-owned shape both
//     backends share.
//
// # The split (issue #1989)
//
//   - Besu backend: the primary/acceptance backend, implemented with the Week-6 integration milestone.
//   - fabricx+EVM gateway backend: booting the gateway node and its topology.
//
// Both implement this same Backend, so the fungible dlog test bodies run unchanged on either by
// selecting the backend. The deployed config must carry the token.tms.<id>.services.network.evm.*
// block the driver reads, including the endorser Ethereum-address to FSC identity registry, threshold
// and allowlist (design §6.1/§10).
package nwo

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package statedelta defines the StateDelta type and the translator that turns validated token
// actions into a StateDelta.
//
// A StateDelta is the EVM backend artifact, analogous to Fabric's RWSet: it carries the anchor, a
// single spentRefs list (token IDs for graph-revealing drivers, serial numbers for graph-hiding
// ones, disambiguated by the contract's graphHiding flag), the new outputs, metadata, and the
// SHA-256 token-request and public-parameters hashes with the public-parameters version. The
// translator (Phase 1.3/3) mirrors token/services/network/common/rws/translator and must produce
// byte-identical deltas across endorsers.
package statedelta

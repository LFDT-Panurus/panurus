/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statedelta

import (
	"bytes"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// OutputToken is a newly created token. It carries two keys (both derived off-chain; the contract
// treats them as opaque): the addressable id and the content-bound marker.
type OutputToken struct {
	// TokenID is keccak256(abi.encode(anchor, index)); the addressable storage key used by queries.
	TokenID [32]byte
	// SNMarker is the content-bound serial-number marker,
	// keccak256(abi.encode(anchor, index, keccak256(TokenData))). Recorded when the output is
	// created; a graph-revealing spend references it so a spender cannot substitute different bytes
	// at the same (anchor, index). Zero for graph-hiding drivers, which spend by serial number.
	SNMarker [32]byte
	// TokenData is the serialized token as produced by the token driver action.
	TokenData []byte
}

// StateDelta is the EVM backend artifact produced by translating a validated token request. It is
// the input to TokenState.applyStateDelta and the message endorsers sign via EIP-712. This Go field
// set matches the Solidity struct and the EIP-712 type exactly; see the eip712 package for the
// encoding.
//
// It uses a single SpentRefs list (Angelo's one-list steer): the contract interprets the refs by its
// graphHiding flag, so exactly one interpretation applies per TokenState (each serves one driver).
type StateDelta struct {
	// Anchor is the token-request anchor, SHA-256(nonce||creator). It is NOT the Ethereum tx hash.
	Anchor [32]byte

	// SpentRefs is the single list of consumed references. Graph-revealing: content-bound output
	// markers (keys.OutputSNMarker) that must have been recorded at creation and get marked spent;
	// binding the content prevents spending forged bytes at a real (anchor, index). Graph-hiding:
	// serial numbers (keys.SpentRefForSerial) that must not already exist and get recorded.
	SpentRefs [][32]byte

	// Outputs are the newly created (non-redeem) tokens, in deterministic counter order.
	Outputs []OutputToken

	// MetadataKeys and MetadataVals are aligned (same length). MetadataKeys is sorted ascending so
	// every endorser produces byte-identical deltas.
	MetadataKeys [][32]byte
	MetadataVals [][]byte

	// TokenRequestHash is SHA-256 of the token request; it matches the hash the rest of the SDK
	// computes, so finality's token-request-hash comparison lines up.
	TokenRequestHash [32]byte

	// PublicParamsHash is SHA-256 of the public parameters used to validate the request, and
	// PublicParamsVersion must equal the TokenState's current version at apply time.
	PublicParamsHash    [32]byte
	PublicParamsVersion uint64

	// IsSetup marks a public-parameters update delta. When set, SpentRefs and Outputs are empty and
	// SetupParameters carries the new public parameters.
	IsSetup         bool
	SetupParameters []byte
}

// Validate checks the structural invariants a well-formed StateDelta must satisfy. The translator
// and endorsers use it to fail fast rather than emit or sign a malformed delta.
//
// Beyond shape checks, it enforces two invariants that protect the signing path:
//   - SetupParameters is present iff IsSetup. The field is covered by the EIP-712 digest, so a
//     non-setup delta smuggling setup bytes would be signed by endorsers while the contract ignores
//     it — refuse it instead.
//   - MetadataKeys are strictly ascending (the frozen §4.4 canonicalization). Unsorted keys mean the
//     emitting translator is broken (endorsers would produce different bytes and signatures would
//     not assemble); duplicate keys would make the on-chain write order ambiguous.
func (d *StateDelta) Validate() error {
	if len(d.MetadataKeys) != len(d.MetadataVals) {
		return errors.Errorf("metadata keys/values length mismatch: %d != %d", len(d.MetadataKeys), len(d.MetadataVals))
	}
	for i := 1; i < len(d.MetadataKeys); i++ {
		if bytes.Compare(d.MetadataKeys[i-1][:], d.MetadataKeys[i][:]) >= 0 {
			return errors.Errorf("metadata keys must be strictly ascending (canonical order), violated at index %d", i)
		}
	}
	if d.IsSetup {
		if len(d.SpentRefs) != 0 || len(d.Outputs) != 0 {
			return errors.Errorf("setup delta must carry no spent refs or outputs")
		}
		if len(d.SetupParameters) == 0 {
			return errors.Errorf("setup delta must carry the new public parameters")
		}
	} else if len(d.SetupParameters) != 0 {
		return errors.Errorf("non-setup delta must not carry setup parameters")
	}

	return nil
}

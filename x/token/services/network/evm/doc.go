/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package evm implements an Ethereum/EVM network driver for the Token SDK.
//
// It follows Approach 2 (pre-order execution with FSC endorsers): FSC nodes validate token
// requests off-chain in Go, sign the resulting state transition with secp256k1 keys (EIP-712),
// and an on-chain contract verifies the endorser signatures plus spent/existence constraints
// before applying the transition. See eth_network_driver_design.md for the full design and
// eth_network_driver_implementation_plan.md for the build sequence.
//
// The package is organized into focused sub-packages:
//
//	client      EVM JSON-RPC client abstraction and the local Address/Hash value types
//	crypto      Keccak256 and SHA-256 primitives (SHA-256 matches the SDK's Hashable)
//	keys        the KeyTranslator that derives on-chain object keys (token IDs, etc.)
//	statedelta  the StateDelta type and the translator from validated actions to a StateDelta
//	eip712      the EIP-712 domain, type hashing, digest and secp256k1 signer/verifier
//	endorsement the endorsement service provider, initiator and responder views
//	finality    transaction finality tracking over the EVM node/gateway
//	pp          the public-parameters version keeper
//	contracts   the Solidity sources (EndorsementVerifier, TokenState) and deploy scripts
//
// Constraint: this driver must not link go-ethereum, whose license is a hard blocker for the
// project. All Ethereum primitives (hashing, addresses, signing, RLP) are provided locally or by
// permissively-licensed libraries. depguard_test.go enforces this.
package evm

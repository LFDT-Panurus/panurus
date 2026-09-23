/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package eip712 implements the EIP-712 signing envelope for endorser signatures over a StateDelta.
//
// It provides the domain separator (bound to chainId and the TokenState contract address), the
// StateDelta type hashing, the final digest, and the secp256k1 signer/verifier. The digest and
// type hashing use Keccak256 and must match the Solidity contract's recomputation exactly.
// Endorsers recompute the digest from their own validated StateDelta and never blind-sign an
// initiator-supplied digest. Hashing lands in Phase 1.4; the signer in Phase 3/4.
package eip712

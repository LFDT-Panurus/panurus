/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package endorsement implements the EVM endorsement flow: collecting a threshold of EIP-712 endorser
// signatures over a StateDelta before a transaction is submitted on-chain (design §6).
//
// It mirrors token/services/network/fabric/endorsement/fsc in shape, but does not reuse it: EVM has
// no Fabric transaction, RWSet or MSP, so the pieces are rebuilt on the backend-agnostic FSC session
// primitives.
//
//   - Registry (registry.go) binds each endorser's Ethereum address to its FSC view.Identity, both
//     directions: the address is what the contract recovers, the identity is how the initiator routes
//     the request.
//   - Responder (responder.go) is the endorser side: receive → authorize (allowlist, the EVM analog
//     of the Fabric MSP/ACL check) → validate the request against on-chain state (ledger.go, getToken
//     at a finalized block tag) → translate to a StateDelta → sign its EIP-712 digest → reply with
//     both. It recomputes the digest from the validated actions and never signs one handed to it
//     (design §4.5).
//   - Initiator (initiator.go) is the collector side: open a session to each registered endorser,
//     gather replies, and count a signature only after recovering it to a distinct registered endorser
//     over the digest of the delta that came back with it, mirroring the contract's threshold and
//     distinct-signer rules.
//   - DeltaFactory (delta.go) is the responder's validate-and-translate path, the one every endorser
//     runs so they all produce byte-identical deltas (the §4.4 determinism guarantee), and Service
//     (service.go) is the per-TMS entry point RequestApproval drives.
//
// Both directions of the wire follow the same principle: the party that does the work is the party
// that decides what it means. The request carries no precomputed digest, so endorsers recompute
// rather than blind-sign; the response carries the delta, so the initiator relays rather than
// revalidates. That is the shape Fabric already has, where the request travels as transient data and
// the RWSet comes back inside the endorsers' proposal responses.
//
// The end-to-end guarantee is pinned by the gate (gate_test.go + contracts/test/Endorsement2ofN.t.sol):
// a 2-of-3 quorum assembled over in-memory sessions verifies on the EndorsementVerifier on-chain.
package endorsement

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package endorsement implements the EVM endorsement flow: collecting EIP-712 endorser signatures
// over a StateDelta before a transaction is submitted on-chain.
//
// It mirrors token/services/network/fabric/endorsement/fsc: a lazy service provider keyed by
// TMSID, an initiator view that collects endorsements from FSC identities, and a responder view
// that validates the request, translates it to a StateDelta, and signs it. Endorsers are bound to
// both an FSC view.Identity (for routing) and an Ethereum address (for on-chain recovery); the
// identity registry lives here. Implemented in Phase 4.
package endorsement

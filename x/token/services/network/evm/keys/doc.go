/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package keys implements the EVM KeyTranslator: the single source of truth for deriving on-chain
// object keys (token IDs, spent markers, metadata keys) from token-request concepts.
//
// It is the EVM analog of token/services/network/common/rws/keys.Translator. The derivations here
// must be reproduced exactly by the Solidity TokenState contract, so the initiator, every
// endorser and the contract agree byte-for-byte on every key. Keys use Keccak256 over abi.encode
// with fixed per-class prefixes for domain separation. Implemented in Phase 1.3.
package keys

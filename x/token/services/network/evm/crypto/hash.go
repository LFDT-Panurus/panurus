/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package crypto provides the hashing primitives used by the EVM network driver.
//
// Two hash functions are used across the driver, and they are NOT interchangeable:
//
//   - Keccak256 is the EVM-native hash. It backs on-chain object keys (token IDs, spent
//     markers, metadata keys) and the EIP-712 digest, so anything the Solidity contracts
//     recompute must use Keccak256.
//   - SHA256 backs the values that flow through the rest of the Token SDK, namely the
//     token-request hash and the public-parameters hash. The SDK computes those with SHA-256
//     (see token/services/utils.Hashable), so the driver must match to stay interoperable with
//     finality and public-parameters checks.
//
// This package deliberately does not depend on go-ethereum: its license is a hard blocker for
// the project. Keccak comes from golang.org/x/crypto/sha3.
package crypto

import (
	"crypto/sha256"

	"golang.org/x/crypto/sha3"
)

// HashLength is the length in bytes of a Keccak256 or SHA-256 digest.
const HashLength = 32

// Keccak256 returns the Keccak-256 digest of the concatenation of data.
//
// This is the pre-standardization Keccak used by Ethereum, not FIPS-202 SHA3-256; the two
// produce different digests for the same input.
func Keccak256(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, b := range data {
		// hash.Hash.Write is documented never to return an error.
		_, _ = h.Write(b)
	}

	return h.Sum(nil)
}

// Keccak256Hash returns the Keccak-256 digest of the concatenation of data as a fixed-size array.
func Keccak256Hash(data ...[]byte) [HashLength]byte {
	var out [HashLength]byte
	copy(out[:], Keccak256(data...))

	return out
}

// SHA256 returns the plain SHA-256 digest of data.
//
// SHA-256 (not Keccak256) is used for the two values that must line up with hashes the rest of the
// Token SDK computes and compares: the token-request hash and the public-parameters hash. This
// equals the SDK's token-request hash for all inputs (CommitTokenRequest applies plain SHA-256), and
// the SDK's public-parameters hash (utils.Hashable.Raw()) for every non-empty input. The one
// documented difference: Hashable.Raw() returns nil for empty input whereas this returns SHA-256("");
// public parameters and token requests are never empty, so the distinction never arises in practice.
func SHA256(data []byte) []byte {
	sum := sha256.Sum256(data)

	return sum[:]
}

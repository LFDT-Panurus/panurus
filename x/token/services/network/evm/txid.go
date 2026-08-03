/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// NonceLength is the size of a generated token-request nonce, matching the 24 bytes FSC generates.
const NonceLength = 24

// computeAnchor derives the token-request anchor from a nonce and a creator identity:
// SHA-256(len(nonce) ‖ nonce ‖ creator), hex encoded (design §5.3).
//
// The nonce is length prefixed rather than plainly concatenated, which is a deliberate deviation from
// Fabric's protoutil.ComputeTxID (it appends the two directly). Plain concatenation is ambiguous: a
// different split of the same bytes between nonce and creator yields the same anchor, and since the
// anchor is the contract's replay key, a collision would make a legitimate transaction revert as
// AnchorAlreadyProcessed. Nothing requires byte-parity with Fabric here, because the anchor is
// produced and consumed only by this driver, so the safer derivation is used.
//
// The result is hex of exactly 32 bytes, so keys.AnchorFromTxID decodes it back.
func computeAnchor(nonce, creator []byte) string {
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(nonce))) // #nosec G115 -- a nonce is never 4 GiB

	buf := make([]byte, 0, len(prefix)+len(nonce)+len(creator))
	buf = append(buf, prefix[:]...)
	buf = append(buf, nonce...)
	buf = append(buf, creator...)

	return hex.EncodeToString(crypto.SHA256(buf))
}

// generateNonce returns a fresh cryptographically random nonce.
func generateNonce() ([]byte, error) {
	nonce := make([]byte, NonceLength)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.Wrap(err, "failed to generate a token-request nonce")
	}

	return nonce, nil
}

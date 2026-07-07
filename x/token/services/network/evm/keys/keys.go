/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package keys

import (
	"encoding/binary"
	"encoding/hex"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
)

// AnchorLength is the byte length of a token-request anchor (a SHA-256 digest).
const AnchorLength = 32

// Domain separators for the different key classes. A single-byte prefix keeps the pre-images of
// distinct key classes disjoint, mirroring the 1-byte-code scheme used by the SDK's
// HashedKeyTranslator, so a serial number can never collide with a metadata key, etc.
const (
	domainIssueMetadata    byte = 0x01
	domainTransferMetadata byte = 0x02
	domainSerialNumber     byte = 0x03
)

// ComputeTokenID returns the addressable on-chain token id for the output at index of the
// transaction identified by anchor: keccak256(abi.encode(anchor, index)). It is the storage key for
// the token bytes (tokens[id]) and what QueryTokens/AreTokensSpent resolve a token.ID to. It does NOT
// bind the token content, so it is not used as a spend reference; see OutputSNMarker.
//
// The contract reproduces this for storage. abi.encode(bytes32, uint256) is the 32-byte anchor
// followed by the 32-byte big-endian index; index is a uint64 here, which occupies the low 8 bytes.
func ComputeTokenID(anchor [AnchorLength]byte, index uint64) [32]byte {
	var buf [64]byte
	copy(buf[:32], anchor[:])
	binary.BigEndian.PutUint64(buf[56:], index)

	return crypto.Keccak256Hash(buf[:])
}

// OutputSNMarker returns the content-bound serial-number marker for the output at (anchor, index)
// with the given serialized token bytes: keccak256(abi.encode(anchor, index, keccak256(tokenData))).
//
// It mirrors Fabric's CreateOutputSNKey. Because the SDK validator is stateless (it validates the
// input tokens carried in the request, not the real on-chain ones), the spend reference must bind
// the exact token content; otherwise a spender could present different bytes at a real (anchor,
// index). The marker is recorded when the output is created and referenced by a graph-revealing
// spend, so a forged token yields a marker that was never recorded and the spend is rejected. The
// contract treats it as an opaque bytes32 (existence check only, no derivation).
func OutputSNMarker(anchor [AnchorLength]byte, index uint64, tokenData []byte) [32]byte {
	contentHash := crypto.Keccak256(tokenData)
	var buf [96]byte
	copy(buf[:32], anchor[:])
	binary.BigEndian.PutUint64(buf[56:64], index)
	copy(buf[64:], contentHash)

	return crypto.Keccak256Hash(buf[:])
}

// SpentRefForSerial returns the spent reference for a graph-hiding serial number:
// keccak256(domainSerialNumber || serial).
func SpentRefForSerial(serial []byte) [32]byte {
	return crypto.Keccak256Hash([]byte{domainSerialNumber}, serial)
}

// IssueMetadataKey returns the on-chain key for an issue-action metadata entry:
// keccak256(domainIssueMetadata || subkey).
func IssueMetadataKey(subkey string) [32]byte {
	return crypto.Keccak256Hash([]byte{domainIssueMetadata}, []byte(subkey))
}

// TransferMetadataKey returns the on-chain key for a transfer-action metadata entry:
// keccak256(domainTransferMetadata || subkey).
func TransferMetadataKey(subkey string) [32]byte {
	return crypto.Keccak256Hash([]byte{domainTransferMetadata}, []byte(subkey))
}

// AnchorFromTxID decodes a token-request anchor, a hex-encoded 32-byte value as produced by the
// driver's ComputeTxID, into its byte-array form.
func AnchorFromTxID(txID string) ([AnchorLength]byte, error) {
	var a [AnchorLength]byte
	b, err := hex.DecodeString(txID)
	if err != nil {
		return a, errors.Wrapf(err, "invalid anchor txID [%s]", txID)
	}
	if len(b) != AnchorLength {
		return a, errors.Errorf("invalid anchor length for [%s]: expected %d bytes, got %d", txID, AnchorLength, len(b))
	}
	copy(a[:], b)

	return a, nil
}

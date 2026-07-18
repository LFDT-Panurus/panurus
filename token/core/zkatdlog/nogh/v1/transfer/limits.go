/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package transfer

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

// Resource limits enforced while deserializing and validating an untrusted transfer action.
//
// These constants are consensus-critical: every peer validating the same action must apply the
// same limits, or otherwise-identical actions could be accepted by one peer and rejected by
// another, breaking endorsement determinism. Changing any value below is a breaking protocol
// change and requires a coordinated upgrade of all validating peers.
const (
	// MaxInputs bounds the number of spent-token inputs in a single transfer action.
	MaxInputs = 256
	// MaxOutputs bounds the number of newly created outputs in a single transfer action.
	MaxOutputs = 256
	// MaxMetadataEntries bounds the number of metadata entries attached to a transfer action.
	MaxMetadataEntries = 64
	// MaxMetadataKeyBytes bounds the length of a single metadata key.
	MaxMetadataKeyBytes = 256
	// MaxMetadataValueBytes bounds the length of a single metadata value.
	MaxMetadataValueBytes = 4 << 10 // 4 KiB
	// MaxProofBytes bounds the length of the action's zero-knowledge proof, checked before the
	// proof is handed to the bulletproof/CSP verifier for deserialization.
	MaxProofBytes = 128 << 10 // 128 KiB
)

// Typed errors returned when a transfer action exceeds a configured resource limit.
var (
	// ErrTooManyInputs is returned when a transfer action spends more than MaxInputs inputs.
	ErrTooManyInputs = errors.Errorf("transfer action exceeds maximum allowed number of inputs [%d]", MaxInputs)
	// ErrTooManyOutputs is returned when a transfer action creates more than MaxOutputs outputs.
	ErrTooManyOutputs = errors.Errorf("transfer action exceeds maximum allowed number of outputs [%d]", MaxOutputs)
	// ErrTooManyMetadataEntries is returned when a transfer action has more than MaxMetadataEntries metadata entries.
	ErrTooManyMetadataEntries = errors.Errorf("transfer action exceeds maximum allowed number of metadata entries [%d]", MaxMetadataEntries)
	// ErrMetadataKeyTooLarge is returned when a metadata key exceeds MaxMetadataKeyBytes.
	ErrMetadataKeyTooLarge = errors.Errorf("transfer action metadata key exceeds maximum allowed size of %d bytes", MaxMetadataKeyBytes)
	// ErrMetadataValueTooLarge is returned when a metadata value exceeds MaxMetadataValueBytes.
	ErrMetadataValueTooLarge = errors.Errorf("transfer action metadata value exceeds maximum allowed size of %d bytes", MaxMetadataValueBytes)
	// ErrProofTooLarge is returned when the action's proof exceeds MaxProofBytes.
	ErrProofTooLarge = errors.Errorf("transfer action proof exceeds maximum allowed size of %d bytes", MaxProofBytes)
)

// checkMetadataLimits enforces MaxMetadataEntries, MaxMetadataKeyBytes and MaxMetadataValueBytes
// on a deserialized transfer action's metadata map.
func checkMetadataLimits(metadata map[string][]byte) error {
	if len(metadata) > MaxMetadataEntries {
		return ErrTooManyMetadataEntries
	}
	for k, v := range metadata {
		if len(k) > MaxMetadataKeyBytes {
			return ErrMetadataKeyTooLarge
		}
		if len(v) > MaxMetadataValueBytes {
			return ErrMetadataValueTooLarge
		}
	}

	return nil
}

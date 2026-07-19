/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package issue

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

// Resource limits enforced while deserializing and validating an untrusted issue action.
//
// The limits enforced by a given Action are configurable (see driver.ResourceLimits); every peer
// validating the same action must be configured with the same limits, or otherwise-identical
// actions could be accepted by one peer and rejected by another, breaking endorsement
// determinism. Deployments that override the defaults are responsible for keeping every
// validating peer in sync.

// Typed errors returned when an issue action exceeds a configured resource limit.
var (
	// ErrTooManyInputs is returned when an issue action redeems more inputs than allowed.
	ErrTooManyInputs = errors.New("issue action exceeds maximum allowed number of inputs")
	// ErrTooManyOutputs is returned when an issue action issues more outputs than allowed.
	ErrTooManyOutputs = errors.New("issue action exceeds maximum allowed number of outputs")
	// ErrTooManyMetadataEntries is returned when an issue action has more metadata entries than allowed.
	ErrTooManyMetadataEntries = errors.New("issue action exceeds maximum allowed number of metadata entries")
	// ErrMetadataKeyTooLarge is returned when a metadata key exceeds the configured limit.
	ErrMetadataKeyTooLarge = errors.New("issue action metadata key exceeds maximum allowed size")
	// ErrMetadataValueTooLarge is returned when a metadata value exceeds the configured limit.
	ErrMetadataValueTooLarge = errors.New("issue action metadata value exceeds maximum allowed size")
	// ErrProofTooLarge is returned when the action's proof exceeds the configured limit.
	ErrProofTooLarge = errors.New("issue action proof exceeds maximum allowed size")
)

// checkMetadataLimits enforces MaxMetadataEntries, MaxMetadataKeyBytes and MaxMetadataValueBytes
// on a deserialized issue action's metadata map.
func checkMetadataLimits(metadata map[string][]byte, maxEntries, maxKeyBytes, maxValueBytes int) error {
	if len(metadata) > maxEntries {
		return errors.Wrapf(ErrTooManyMetadataEntries, "limit [%d]", maxEntries)
	}
	for k, v := range metadata {
		if len(k) > maxKeyBytes {
			return errors.Wrapf(ErrMetadataKeyTooLarge, "limit [%d] bytes", maxKeyBytes)
		}
		if len(v) > maxValueBytes {
			return errors.Wrapf(ErrMetadataValueTooLarge, "limit [%d] bytes", maxValueBytes)
		}
	}

	return nil
}

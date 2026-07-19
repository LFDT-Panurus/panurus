/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package actions

import "github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

// Resource limits enforced while deserializing and validating an untrusted fabtoken action.
//
// The limits enforced by a given action are configurable (see driver.ResourceLimits); every peer
// validating the same action must be configured with the same limits, or otherwise-identical
// actions could be accepted by one peer and rejected by another, breaking endorsement
// determinism. Deployments that override the defaults are responsible for keeping every
// validating peer in sync.

// Typed errors returned when a fabtoken action exceeds a configured resource limit.
var (
	// ErrTooManyInputs is returned when a transfer action spends more inputs than allowed.
	ErrTooManyInputs = errors.New("action exceeds maximum allowed number of inputs")
	// ErrTooManyOutputs is returned when an action has more outputs than allowed.
	ErrTooManyOutputs = errors.New("action exceeds maximum allowed number of outputs")
	// ErrTooManyMetadataEntries is returned when an action has more metadata entries than allowed.
	ErrTooManyMetadataEntries = errors.New("action exceeds maximum allowed number of metadata entries")
	// ErrMetadataKeyTooLarge is returned when a metadata key exceeds the configured limit.
	ErrMetadataKeyTooLarge = errors.New("action metadata key exceeds maximum allowed size")
	// ErrMetadataValueTooLarge is returned when a metadata value exceeds the configured limit.
	ErrMetadataValueTooLarge = errors.New("action metadata value exceeds maximum allowed size")
)

// checkMetadataLimits enforces MaxMetadataEntries, MaxMetadataKeyBytes and MaxMetadataValueBytes
// on a deserialized action's metadata map.
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

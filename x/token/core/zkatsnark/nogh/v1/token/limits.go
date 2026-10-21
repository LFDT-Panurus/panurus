/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Typed errors returned when an action exceeds a configured resource limit.
// The limits are consensus-relevant, see driver.ResourceLimits.
var (
	// ErrTooManyInputs is returned when an action spends more inputs than allowed.
	ErrTooManyInputs = errors.New("action exceeds maximum allowed number of inputs")
	// ErrTooManyOutputs is returned when an action creates more outputs than allowed.
	ErrTooManyOutputs = errors.New("action exceeds maximum allowed number of outputs")
	// ErrTooManyMetadataEntries is returned when an action has more metadata entries than allowed.
	ErrTooManyMetadataEntries = errors.New("action exceeds maximum allowed number of metadata entries")
	// ErrMetadataKeyTooLarge is returned when a metadata key exceeds the configured limit.
	ErrMetadataKeyTooLarge = errors.New("action metadata key exceeds maximum allowed size")
	// ErrMetadataValueTooLarge is returned when a metadata value exceeds the configured limit.
	ErrMetadataValueTooLarge = errors.New("action metadata value exceeds maximum allowed size")
)

// effectiveLimits returns the configured limits, or the defaults if none were set.
func effectiveLimits(l *driver.ResourceLimits) driver.ResourceLimits {
	if l == nil {
		return driver.DefaultResourceLimits()
	}

	return l.WithDefaults()
}

// checkMetadataLimits enforces the metadata limits on an action's metadata map.
func checkMetadataLimits(metadata map[string][]byte, l driver.ResourceLimits) error {
	if len(metadata) > l.MaxMetadataEntries {
		return errors.Wrapf(ErrTooManyMetadataEntries, "limit [%d]", l.MaxMetadataEntries)
	}
	for k, v := range metadata {
		if len(k) > l.MaxMetadataKeyBytes {
			return errors.Wrapf(ErrMetadataKeyTooLarge, "limit [%d] bytes", l.MaxMetadataKeyBytes)
		}
		if len(v) > l.MaxMetadataValueBytes {
			return errors.Wrapf(ErrMetadataValueTooLarge, "limit [%d] bytes", l.MaxMetadataValueBytes)
		}
	}

	return nil
}

// checkCountLimits enforces the input/output count limits.
func checkCountLimits(inputs, outputs int, l driver.ResourceLimits) error {
	if inputs > l.MaxInputs {
		return errors.Wrapf(ErrTooManyInputs, "limit [%d]", l.MaxInputs)
	}
	if outputs > l.MaxOutputs {
		return errors.Wrapf(ErrTooManyOutputs, "limit [%d]", l.MaxOutputs)
	}

	return nil
}

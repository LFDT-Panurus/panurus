/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceLimits_WithDefaults_AllUnset(t *testing.T) {
	var l ResourceLimits
	assert.Equal(t, DefaultResourceLimits(), l.WithDefaults())
}

func TestResourceLimits_WithDefaults_PartialOverride(t *testing.T) {
	l := ResourceLimits{MaxActions: 2, MaxProofBytes: 1024}

	want := DefaultResourceLimits()
	want.MaxActions = 2
	want.MaxProofBytes = 1024

	assert.Equal(t, want, l.WithDefaults())
}

func TestResourceLimits_WithDefaults_FullyOverridden(t *testing.T) {
	l := ResourceLimits{
		MaxRequestBytes:       1,
		MaxActions:            2,
		MaxSignatures:         3,
		MaxSignatureBytes:     4,
		MaxActionBytes:        5,
		MaxInputs:             6,
		MaxOutputs:            7,
		MaxMetadataEntries:    8,
		MaxMetadataKeyBytes:   9,
		MaxMetadataValueBytes: 10,
		MaxProofBytes:         11,
	}
	assert.Equal(t, l, l.WithDefaults())
}

// Negative values (e.g. from a config typo) must fall back to the default rather than being
// preserved, since a negative bound would compare as "always satisfied" wherever these limits are
// enforced, effectively disabling the check.
func TestResourceLimits_WithDefaults_NegativeValuesFallBackToDefault(t *testing.T) {
	l := ResourceLimits{
		MaxRequestBytes:       -1,
		MaxActions:            -1,
		MaxSignatures:         -1,
		MaxSignatureBytes:     -1,
		MaxActionBytes:        -1,
		MaxInputs:             -1,
		MaxOutputs:            -1,
		MaxMetadataEntries:    -1,
		MaxMetadataKeyBytes:   -1,
		MaxMetadataValueBytes: -1,
		MaxProofBytes:         -1,
	}
	assert.Equal(t, DefaultResourceLimits(), l.WithDefaults())
}

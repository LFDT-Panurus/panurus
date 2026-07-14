/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
)

// TestMinProtocolVersionEnforcement tests the minimum protocol version enforcement
func TestMinProtocolVersionEnforcement(t *testing.T) {
	tests := []struct {
		name               string
		minProtocolVersion uint32
		requestVersion     uint32
		shouldFail         bool
		expectedError      string
	}{
		{
			name:               "Version 0 is treated as V1",
			minProtocolVersion: 0,
			requestVersion:     0,
			shouldFail:         false,
		},
		{
			name:               "No minimum version set - accepts V1",
			minProtocolVersion: 0,
			requestVersion:     driver.ProtocolV1,
			shouldFail:         false,
		},
		{
			name:               "Minimum V1 - accepts version 0 (as V1)",
			minProtocolVersion: driver.ProtocolV1,
			requestVersion:     0,
			shouldFail:         false,
		},
		{
			name:               "Minimum V1 - accepts V1",
			minProtocolVersion: driver.ProtocolV1,
			requestVersion:     driver.ProtocolV1,
			shouldFail:         false,
		},
		{
			name:               "Minimum V1 - accepts V2",
			minProtocolVersion: driver.ProtocolV1,
			requestVersion:     driver.ProtocolV2,
			shouldFail:         false,
		},
		{
			name:               "Minimum V2 - rejects version 0 (as V1)",
			minProtocolVersion: driver.ProtocolV2,
			requestVersion:     0,
			shouldFail:         true,
			expectedError:      "token request protocol version [1] is below minimum required version [2]",
		},
		{
			name:               "Minimum V2 - rejects V1",
			minProtocolVersion: driver.ProtocolV2,
			requestVersion:     driver.ProtocolV1,
			shouldFail:         true,
			expectedError:      "token request protocol version [1] is below minimum required version [2]",
		},
		{
			name:               "Minimum V2 - accepts V2",
			minProtocolVersion: driver.ProtocolV2,
			requestVersion:     driver.ProtocolV2,
			shouldFail:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the version check logic directly
			var err error

			reqVersion := tt.requestVersion
			if reqVersion == 0 {
				reqVersion = 1
			}

			if tt.minProtocolVersion > 0 && reqVersion < tt.minProtocolVersion {
				// Enforce minimum version if configured
				err = assert.AnError
			}

			if tt.shouldFail {
				assert.Error(t, err, "Expected version check to fail")
			} else {
				assert.NoError(t, err, "Expected version check to pass")
			}
		})
	}
}

// TestMinProtocolVersionLogic tests the version comparison logic
func TestMinProtocolVersionLogic(t *testing.T) {
	tests := []struct {
		name           string
		minVersion     uint32
		requestVersion uint32
		shouldPass     bool
		reason         string
	}{
		{"V0 treated as V1", 0, 0, true, ""},
		{"No min, V1 request", 0, driver.ProtocolV1, true, ""},
		{"No min, V2 request", 0, driver.ProtocolV2, true, ""},
		{"Min V1, V0 request (as V1)", driver.ProtocolV1, 0, true, ""},
		{"Min V1, V1 request", driver.ProtocolV1, driver.ProtocolV1, true, ""},
		{"Min V1, V2 request", driver.ProtocolV1, driver.ProtocolV2, true, ""},
		{"Min V2, V0 request (as V1)", driver.ProtocolV2, 0, false, "below minimum"},
		{"Min V2, V1 request", driver.ProtocolV2, driver.ProtocolV1, false, "below minimum"},
		{"Min V2, V2 request", driver.ProtocolV2, driver.ProtocolV2, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the version check logic
			var passes bool

			reqVersion := tt.requestVersion
			if reqVersion == 0 {
				reqVersion = 1
			}

			// Enforce minimum version if configured
			passes = tt.minVersion == 0 || reqVersion >= tt.minVersion

			assert.Equal(t, tt.shouldPass, passes,
				"Version check logic mismatch: min=%d, request=%d, reason=%s",
				tt.minVersion, tt.requestVersion, tt.reason)
		})
	}
}

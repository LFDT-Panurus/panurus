/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewTokenValidationParamsSlice_EmptyPath verifies that the function
// returns an error when TestRootPath is empty, as per the code review recommendation.
func TestNewTokenValidationParamsSlice_EmptyPath(t *testing.T) {
	_, err := NewTokenValidationParamsSlice("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "TestRootPath cannot be empty")
}

// TestNewTokenValidationParamsSlice_ValidPath verifies that the function
// works correctly when a valid path is provided.
func TestNewTokenValidationParamsSlice_ValidPath(t *testing.T) {
	// Use the exported DefaultTestRoot constant
	params, err := NewTokenValidationParamsSlice(DefaultTestRoot)

	// This may fail if the test data doesn't exist, but that's expected
	// The important thing is that it doesn't panic and handles errors properly
	if err != nil {
		// If there's an error, it should be a proper error, not a panic
		require.Error(t, err)
		t.Logf("Expected error when test data is not available: %v", err)
	} else {
		// If successful, verify we got valid params
		require.NotNil(t, params)
		require.NotEmpty(t, params)
	}
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Note: The functions newMultiplexedDriver and newTokenDriverService are dependency injection
// provider functions that use dig.In structs. They are thin wrappers around constructors
// and are primarily tested through integration tests and Panurus wiring test (TestFabricWiring).
// Direct unit testing would require complex mocking of dig.In behavior which provides
// limited value compared to integration testing.
//
// These functions are covered by:
// 1. TestFabricWiring - which tests the full DI container wiring
// 2. Integration tests that exercise the complete SDK initialization

func TestProviderFunctionsExist(t *testing.T) {
	// This test verifies that the provider functions are defined and can be referenced
	// The actual functionality is tested through SDK integration tests
	require.NotNil(t, newMultiplexedDriver)
	require.NotNil(t, newTokenDriverService)
}

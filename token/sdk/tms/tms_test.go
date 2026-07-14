/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/sdk/tms"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPostInitializer(t *testing.T) {
	tokensProvider := &tokens.ServiceManager{}
	networkProvider := &network.Provider{}
	ownerManager := &ttx.ServiceManager{}
	auditorManager := &auditor.ServiceManager{}

	initializer, err := tms.NewPostInitializer(tokensProvider, networkProvider, ownerManager, auditorManager)

	require.NoError(t, err)
	require.NotNil(t, initializer)
}

func TestNewPostInitializer_WithNilProviders(t *testing.T) {
	// Test that initializer can be created even with nil providers
	initializer, err := tms.NewPostInitializer(nil, nil, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, initializer)
}

func TestNewPostInitializer_Structure(t *testing.T) {
	tokensProvider := &tokens.ServiceManager{}
	networkProvider := &network.Provider{}
	ownerManager := &ttx.ServiceManager{}
	auditorManager := &auditor.ServiceManager{}

	initializer, err := tms.NewPostInitializer(tokensProvider, networkProvider, ownerManager, auditorManager)

	require.NoError(t, err)
	require.NotNil(t, initializer)

	// Verify the structure is properly initialized
	// Note: fields are private, so we can only verify the initializer was created
	assert.NotNil(t, initializer)
}

func TestNewPostInitializer_MultipleInstances(t *testing.T) {
	tokensProvider1 := &tokens.ServiceManager{}
	networkProvider1 := &network.Provider{}
	ownerManager1 := &ttx.ServiceManager{}
	auditorManager1 := &auditor.ServiceManager{}

	tokensProvider2 := &tokens.ServiceManager{}
	networkProvider2 := &network.Provider{}
	ownerManager2 := &ttx.ServiceManager{}
	auditorManager2 := &auditor.ServiceManager{}

	initializer1, err := tms.NewPostInitializer(tokensProvider1, networkProvider1, ownerManager1, auditorManager1)
	require.NoError(t, err)

	initializer2, err := tms.NewPostInitializer(tokensProvider2, networkProvider2, ownerManager2, auditorManager2)
	require.NoError(t, err)

	// Verify they are different instances
	assert.NotSame(t, initializer1, initializer2)
}

// Note: The PostInit method requires a fully initialized TMS and database services,
// which are complex to mock. It performs the following operations:
// 1. Restores owner database for the TMS
// 2. Restores auditor database for the TMS
// 3. Sets supported token formats
// These operations are better tested through integration tests where all dependencies
// are properly initialized. The method is tested through Panurus integration tests.

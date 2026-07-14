/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package db

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/common/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAuditorCheckServiceProvider verifies that NewAuditorCheckServiceProvider
// correctly initializes an AuditorCheckServiceProvider with the given dependencies.
func TestNewAuditorCheckServiceProvider(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	provider := NewAuditorCheckServiceProvider(tmsProvider, networkProvider, checkers)

	assert.NotNil(t, provider)
	assert.Equal(t, tmsProvider, provider.tmsProvider)
	assert.Equal(t, networkProvider, provider.networkProvider)
	assert.Equal(t, checkers, provider.checkers)
}

// TestAuditorCheckServiceProvider_CheckService verifies that CheckService
// creates a check service with default and custom checkers.
func TestAuditorCheckServiceProvider_CheckService(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	provider := NewAuditorCheckServiceProvider(tmsProvider, networkProvider, checkers)

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	// Note: This test will return a service but we can't fully test it without mocking the dependencies
	// The actual CheckService creation requires valid auditdb and tokens services
	service, err := provider.CheckService(tmsID, nil, nil)

	// We expect the service to be created even with nil dependencies
	// as the common.NewChecksService should handle it
	assert.NotNil(t, service)
	assert.NoError(t, err)
}

// TestNewOwnerCheckServiceProvider verifies that NewOwnerCheckServiceProvider
// correctly initializes an OwnerCheckServiceProvider with the given dependencies.
func TestNewOwnerCheckServiceProvider(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	provider := NewOwnerCheckServiceProvider(tmsProvider, networkProvider, checkers)

	assert.NotNil(t, provider)
	assert.Equal(t, tmsProvider, provider.tmsProvider)
	assert.Equal(t, networkProvider, provider.networkProvider)
	assert.Equal(t, checkers, provider.checkers)
}

// TestOwnerCheckServiceProvider_CheckService verifies that CheckService
// creates a check service with default and custom checkers.
func TestOwnerCheckServiceProvider_CheckService(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	provider := NewOwnerCheckServiceProvider(tmsProvider, networkProvider, checkers)

	tmsID := token.TMSID{
		Network:   "test-network",
		Channel:   "test-channel",
		Namespace: "test-namespace",
	}

	// Note: This test will return a service but we can't fully test it without mocking the dependencies
	service, err := provider.CheckService(tmsID, nil, nil)

	// We expect the service to be created even with nil dependencies
	assert.NotNil(t, service)
	assert.NoError(t, err)
}

// TestAuditorCheckServiceProvider_WithMultipleCheckers verifies that the provider
// correctly handles multiple custom checkers.
func TestAuditorCheckServiceProvider_WithMultipleCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}

	// Test with multiple checkers
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker3", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	provider := NewAuditorCheckServiceProvider(tmsProvider, networkProvider, checkers)
	require.NotNil(t, provider)
	assert.Len(t, provider.checkers, 3)
}

// TestOwnerCheckServiceProvider_WithMultipleCheckers verifies that the provider
// correctly handles multiple custom checkers.
func TestOwnerCheckServiceProvider_WithMultipleCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}

	// Test with multiple checkers
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker2", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
		{Name: "checker3", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	provider := NewOwnerCheckServiceProvider(tmsProvider, networkProvider, checkers)
	require.NotNil(t, provider)
	assert.Len(t, provider.checkers, 3)
}

// TestAuditorCheckServiceProvider_WithEmptyCheckers verifies that the provider
// works correctly with an empty checkers list.
func TestAuditorCheckServiceProvider_WithEmptyCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{}

	provider := NewAuditorCheckServiceProvider(tmsProvider, networkProvider, checkers)
	require.NotNil(t, provider)
	assert.Empty(t, provider.checkers)
}

// TestOwnerCheckServiceProvider_WithEmptyCheckers verifies that the provider
// works correctly with an empty checkers list.
func TestOwnerCheckServiceProvider_WithEmptyCheckers(t *testing.T) {
	tmsProvider := &mock.TokenManagementServiceProvider{}
	networkProvider := &mock.NetworkProvider{}
	checkers := []common.NamedChecker{}

	provider := NewOwnerCheckServiceProvider(tmsProvider, networkProvider, checkers)
	require.NotNil(t, provider)
	assert.Empty(t, provider.checkers)
}

// TestAuditorCheckServiceProvider_WithNilProviders verifies that the provider
// can be created with nil TMS and network providers.
func TestAuditorCheckServiceProvider_WithNilProviders(t *testing.T) {
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	// Test with nil providers - should still create the provider
	provider := NewAuditorCheckServiceProvider(nil, nil, checkers)
	require.NotNil(t, provider)
	assert.Nil(t, provider.tmsProvider)
	assert.Nil(t, provider.networkProvider)
	assert.Len(t, provider.checkers, 1)
}

// TestOwnerCheckServiceProvider_WithNilProviders verifies that the provider
// can be created with nil TMS and network providers.
func TestOwnerCheckServiceProvider_WithNilProviders(t *testing.T) {
	checkers := []common.NamedChecker{
		{Name: "checker1", Checker: func(ctx context.Context) ([]string, error) { return nil, nil }},
	}

	// Test with nil providers - should still create the provider
	provider := NewOwnerCheckServiceProvider(nil, nil, checkers)
	require.NotNil(t, provider)
	assert.Nil(t, provider.tmsProvider)
	assert.Nil(t, provider.networkProvider)
	assert.Len(t, provider.checkers, 1)
}

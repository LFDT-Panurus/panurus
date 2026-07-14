/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Manual mocks for interfaces that reference same-package types.
// Cannot use counterfeiter because it would create import cycle: token (tests) → token/mock → token

// mockServiceProvider mocks ServiceProvider interface
type mockServiceProvider struct {
	service any
	err     error
}

func (m *mockServiceProvider) GetService(v any) (any, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.service, nil
}

// mockNormalizer mocks Normalizer interface (references *ServiceOptions from this package)
type mockNormalizer struct {
	normalizeFunc func(*ServiceOptions) (*ServiceOptions, error)
	normalizeErr  error
}

func (m *mockNormalizer) Normalize(opt *ServiceOptions) (*ServiceOptions, error) {
	if m.normalizeErr != nil {
		return nil, m.normalizeErr
	}
	if m.normalizeFunc != nil {
		return m.normalizeFunc(opt)
	}

	return opt, nil
}

// mockCertificationClientProvider mocks CertificationClientProvider interface (references *ManagementService from this package)
type mockCertificationClientProvider struct {
	cc  driver.CertificationClient
	err error
}

func (m *mockCertificationClientProvider) New(ctx context.Context, tms *ManagementService) (driver.CertificationClient, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.cc != nil {
		return m.cc, nil
	}

	return &mock.CertificationClient{}, nil
}

// mockSelectorManager mocks SelectorManager interface (references Selector from this package)
type mockSelectorManager struct{}

func (m *mockSelectorManager) NewSelector(id string) (Selector, error) {
	return nil, nil
}

func (m *mockSelectorManager) Unlock(ctx context.Context, id string) error {
	return nil
}

func (m *mockSelectorManager) Close(id string) error {
	return nil
}

// mockSelectorManagerProvider mocks SelectorManagerProvider interface (references *ManagementService from this package)
type mockSelectorManagerProvider struct {
	sm  SelectorManager
	err error
}

func (m *mockSelectorManagerProvider) SelectorManager(tms *ManagementService) (SelectorManager, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.sm != nil {
		return m.sm, nil
	}

	return &mockSelectorManager{}, nil
}

// mockTokenManagerServiceProvider mocks driver.TokenManagerServiceProvider interface
type mockTokenManagerServiceProvider struct {
	updateCallCount int
	updateErr       error
	getTMSErr       error
	tms             driver.TokenManagerService
}

func (m *mockTokenManagerServiceProvider) GetTokenManagerService(opts driver.ServiceOptions) (driver.TokenManagerService, error) {
	if m.getTMSErr != nil {
		return nil, m.getTMSErr
	}

	return m.tms, nil
}

func (m *mockTokenManagerServiceProvider) Update(opts driver.ServiceOptions) error {
	m.updateCallCount++

	return m.updateErr
}

// mockVaultProvider mocks VaultProvider interface
type mockVaultProvider struct {
	vault driver.Vault
	err   error
}

func (m *mockVaultProvider) Vault(network string, channel string, namespace string) (driver.Vault, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.vault, nil
}

// TestNewManagementServiceProvider verifies constructor creates provider correctly
func TestNewManagementServiceProvider(t *testing.T) {
	tmsProvider := &mockTokenManagerServiceProvider{}
	normalizer := &mockNormalizer{}
	vaultProvider := &mockVaultProvider{}
	certProvider := &mockCertificationClientProvider{}
	selectorProvider := &mockSelectorManagerProvider{}

	provider := NewManagementServiceProvider(
		tmsProvider,
		normalizer,
		vaultProvider,
		certProvider,
		selectorProvider,
	)

	assert.NotNil(t, provider)
	assert.Equal(t, tmsProvider, provider.tmsProvider)
	assert.Equal(t, normalizer, provider.normalizer)
	assert.Equal(t, vaultProvider, provider.vaultProvider)
	assert.NotNil(t, provider.services)
}

// TestGetManagementServiceProvider verifies retrieval from service provider
func TestGetManagementServiceProvider(t *testing.T) {
	msp := &ManagementServiceProvider{}
	sp := &mockServiceProvider{service: msp}

	result := GetManagementServiceProvider(sp)

	assert.Equal(t, msp, result)
}

// TestGetManagementServiceProvider_Panic verifies panic on error
func TestGetManagementServiceProvider_Panic(t *testing.T) {
	sp := &mockServiceProvider{err: errors.New("service not found")}

	assert.Panics(t, func() {
		GetManagementServiceProvider(sp)
	})
}

// TestManagementServiceProvider_Update verifies update clears cache
func TestManagementServiceProvider_Update(t *testing.T) {
	tmsProvider := &mockTokenManagerServiceProvider{}
	normalizer := &mockNormalizer{}
	vaultProvider := &mockVaultProvider{}
	certProvider := &mockCertificationClientProvider{}
	selectorProvider := &mockSelectorManagerProvider{}

	tmsProvider.updateErr = nil

	provider := NewManagementServiceProvider(
		tmsProvider,
		normalizer,
		vaultProvider,
		certProvider,
		selectorProvider,
	)

	// Add a cached service
	provider.services["net1ch1ns1"] = &ManagementService{}

	tmsID := TMSID{
		Network:   "net1",
		Channel:   "ch1",
		Namespace: "ns1",
	}

	err := provider.Update(tmsID, []byte("new params"))

	require.NoError(t, err)
	assert.Equal(t, 1, tmsProvider.updateCallCount)

	// Verify cache was cleared
	_, exists := provider.services["net1ch1ns1"]
	assert.False(t, exists)
}

// TestManagementServiceProvider_Update_Error verifies error handling
func TestManagementServiceProvider_Update_Error(t *testing.T) {
	tmsProvider := &mockTokenManagerServiceProvider{}
	normalizer := &mockNormalizer{}
	vaultProvider := &mockVaultProvider{}
	certProvider := &mockCertificationClientProvider{}
	selectorProvider := &mockSelectorManagerProvider{}

	expectedErr := errors.New("update failed")
	tmsProvider.updateErr = expectedErr

	provider := NewManagementServiceProvider(
		tmsProvider,
		normalizer,
		vaultProvider,
		certProvider,
		selectorProvider,
	)

	tmsID := TMSID{Network: "net1"}
	err := provider.Update(tmsID, []byte("params"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed updating tms")
	assert.ErrorIs(t, err, expectedErr)
}

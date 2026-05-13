/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/config"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceUnit(t *testing.T) {
	mockFP := &mocks.FakeFetcherProvider{}
	mockLSM := &mocks.FakeTokenLockStoreServiceManager{}
	mockCP := &mocks.FakeConfigProvider{}
	metricsProvider, _ := setupMetricsMocks()

	svc, err := sherdlock.NewService(mockFP, mockLSM, mockCP, metricsProvider)
	require.NoError(t, err)
	require.NotNil(t, svc)

	t.Run("Shutdown", func(t *testing.T) {
		svc.Shutdown()
		assert.Equal(t, 0, svc.ManagersCount())
	})

	t.Run("SelectorManager_NilTMS", func(t *testing.T) {
		_, err := svc.SelectorManager(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid tms")
	})

	t.Run("SelectorManager_Success", func(t *testing.T) {
		tmsID := token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}

		// Setup driver TMS mock
		driverTMS := &drivermock.TokenManagerService{}
		mockPPM := &drivermock.PublicParamsManager{}
		driverTMS.PublicParamsManagerReturns(mockPPM)
		mockPP := &drivermock.PublicParameters{}
		mockPP.PrecisionReturns(64)
		mockPPM.PublicParametersReturns(mockPP)

		// Create real ManagementService with mock driver
		tms, err := token.NewManagementService(tmsID, driverTMS, nil, &tokenMockVP{}, nil, nil)
		require.NoError(t, err)

		mockLSM.StoreServiceByTMSIdReturns(nil, nil)
		mockFP.GetFetcherReturns(&mocks.FakeTokenFetcher{}, nil)

		mgr, err := svc.SelectorManager(tms)
		require.NoError(t, err)
		assert.NotNil(t, mgr)
		assert.Equal(t, 1, svc.ManagersCount())
	})

	t.Run("ManagersCount", func(t *testing.T) {
		// New service starts with 0
		svc2, err := sherdlock.NewService(mockFP, mockLSM, mockCP, metricsProvider)
		require.NoError(t, err)
		assert.Equal(t, 0, svc2.ManagersCount())
	})
}

// TestServiceRejectsInvalidConfig pins that an invalid selector configuration
// fails startup instead of being silently reset to defaults. Setting
// maxTokensPerSelection below the (untouched) default maxLocksPerTransaction is
// a "tighten one knob" config that must not resolve back to the laxer defaults:
// NewService must return an error rather than a service running on 10 000
// tokens/selection the operator never asked for.
func TestServiceRejectsInvalidConfig(t *testing.T) {
	mockCP := &mocks.FakeConfigProvider{}
	mockCP.UnmarshalKeyStub = func(_ string, rawVal any) error {
		cfg, ok := rawVal.(*config.Config)
		require.True(t, ok)
		// Below the default maxLocksPerTransaction (5000); maxLocksPerTransaction
		// is left unset so it resolves to that default and violates the invariant.
		cfg.Limits.MaxTokensPerSelection = 3000

		return nil
	}
	metricsProvider, _ := setupMetricsMocks()

	svc, err := sherdlock.NewService(&mocks.FakeFetcherProvider{}, &mocks.FakeTokenLockStoreServiceManager{}, mockCP, metricsProvider)
	require.Error(t, err)
	require.Nil(t, svc)
	assert.Contains(t, err.Error(), "invalid selector configuration")
}

// Minimal VaultProvider mock for NewManagementService
type tokenMockVP struct{}

func (v *tokenMockVP) Vault(network, channel, namespace string) (driver.Vault, error) {
	return &drivermock.Vault{}, nil
}

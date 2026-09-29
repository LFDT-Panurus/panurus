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

// TestServiceRejectsInvalidConfig pins that a genuinely invalid selector
// configuration fails startup instead of being silently reset to defaults. Here
// the operator explicitly sets maxLocksPerTransaction above maxTokensPerSelection,
// which violates the maxLocksPerTransaction <= maxTokensPerSelection invariant no
// matter what defaults would resolve to: NewService must return an error rather
// than run on limits the operator never asked for.
func TestServiceRejectsInvalidConfig(t *testing.T) {
	mockCP := &mocks.FakeConfigProvider{}
	mockCP.UnmarshalKeyStub = func(_ string, rawVal any) error {
		cfg, ok := rawVal.(*config.Config)
		require.True(t, ok)
		// Both explicitly set and mutually inconsistent: more locks than tokens
		// may ever be examined. This is an operator mistake, not a defaulting gap,
		// so it must be rejected rather than clamped.
		cfg.Limits.MaxTokensPerSelection = 3000
		cfg.Limits.MaxLocksPerTransaction = 5000

		return nil
	}
	metricsProvider, _ := setupMetricsMocks()

	svc, err := sherdlock.NewService(&mocks.FakeFetcherProvider{}, &mocks.FakeTokenLockStoreServiceManager{}, mockCP, metricsProvider)
	require.Error(t, err)
	require.Nil(t, svc)
	assert.Contains(t, err.Error(), "invalid selector configuration")
}

// TestServiceAcceptsTightenedTokenLimit pins the fix for the "tighten one knob"
// footgun: lowering maxTokensPerSelection alone (the documented way to harden a
// deployment, see docs/security/selector_resource_limits.md) must not make the
// node refuse to start. maxLocksPerTransaction is left unset, so it resolves to
// the smaller of its own default and the tightened maxTokensPerSelection rather
// than to the larger default that would violate the invariant.
func TestServiceAcceptsTightenedTokenLimit(t *testing.T) {
	mockCP := &mocks.FakeConfigProvider{}
	mockCP.UnmarshalKeyStub = func(_ string, rawVal any) error {
		cfg, ok := rawVal.(*config.Config)
		require.True(t, ok)
		// Below the default maxLocksPerTransaction (5000); maxLocksPerTransaction
		// is deliberately left unset so it must resolve to a value that keeps the
		// config valid.
		cfg.Limits.MaxTokensPerSelection = 1000

		return nil
	}
	metricsProvider, _ := setupMetricsMocks()

	svc, err := sherdlock.NewService(&mocks.FakeFetcherProvider{}, &mocks.FakeTokenLockStoreServiceManager{}, mockCP, metricsProvider)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

// Minimal VaultProvider mock for NewManagementService
type tokenMockVP struct{}

func (v *tokenMockVP) Vault(network, channel, namespace string) (driver.Vault, error) {
	return &drivermock.Vault{}, nil
}

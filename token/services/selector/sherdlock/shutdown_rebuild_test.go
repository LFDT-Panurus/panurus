/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShutdownDoesNotKeepServingStoppedManagers checks that a manager stopped by Shutdown is not
// handed out again. Managers are cached by TMS id, and Shutdown runs on every public-parameters
// reload, so reusing the stopped one would leave its lease cleanup off for the life of the process
// and stale token locks would never be released.
func TestShutdownDoesNotKeepServingStoppedManagers(t *testing.T) {
	mockFP := &mocks.FakeFetcherProvider{}
	mockFP.GetFetcherReturns(&mocks.FakeTokenFetcher{}, nil)
	metricsProvider, _ := setupMetricsMocks()
	svc := sherdlock.NewService(mockFP, &mocks.FakeTokenLockStoreServiceManager{}, &mocks.FakeConfigProvider{}, metricsProvider)

	pp := &drivermock.PublicParameters{}
	pp.PrecisionReturns(64)
	ppm := &drivermock.PublicParamsManager{}
	ppm.PublicParametersReturns(pp)
	driverTMS := &drivermock.TokenManagerService{}
	driverTMS.PublicParamsManagerReturns(ppm)
	tms, err := token.NewManagementService(token.TMSID{Network: "n", Namespace: "ns"}, driverTMS, nil, &tokenMockVP{}, nil, nil)
	require.NoError(t, err)

	before, err := svc.SelectorManager(tms)
	require.NoError(t, err)
	require.Equal(t, 1, svc.ManagersCount())

	svc.Shutdown()
	require.Equal(t, 0, svc.ManagersCount())

	after, err := svc.SelectorManager(tms)
	require.NoError(t, err)
	assert.NotSame(t, before, after, "the stopped manager must not be served again")
	assert.Equal(t, 1, svc.ManagersCount(), "the replacement is running and tracked")

	again, err := svc.SelectorManager(tms)
	require.NoError(t, err)
	assert.Same(t, after, again, "the replacement is cached like any other manager")
	svc.Shutdown()
}

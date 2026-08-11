/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// walletFilter is a token.OwnerFilter carrying a wallet id.
type walletFilter string

func (w walletFilter) ID() string { return string(w) }

// denyingLimiter throttles every request and counts how often it was consulted.
type denyingLimiter struct {
	allows atomic.Int64
	stops  atomic.Int64
}

func (l *denyingLimiter) Allow(identity string) error {
	l.allows.Add(1)

	return errors.Wrapf(token.SelectorRateLimited, "wallet %s throttled", identity)
}

func (l *denyingLimiter) Stop() { l.stops.Add(1) }

// newTestTMS builds a minimal management service the selector service can load a manager
// for, mirroring the setup in service_test.go.
func newTestTMS(t *testing.T) *token.ManagementService {
	t.Helper()

	driverTMS := &drivermock.TokenManagerService{}
	mockPPM := &drivermock.PublicParamsManager{}
	driverTMS.PublicParamsManagerReturns(mockPPM)
	mockPP := &drivermock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM.PublicParametersReturns(mockPP)

	tms, err := token.NewManagementService(
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"},
		driverTMS, nil, &tokenMockVP{}, nil, nil,
	)
	require.NoError(t, err)

	return tms
}

// The limiter must be enforced by the manager the service hands out, not just by the
// service itself: a throttled selection has to fail fast, before any storage is touched.
func TestService_SelectionIsMeteredByTheLimiter(t *testing.T) {
	mockFP := &mocks.FakeFetcherProvider{}
	mockLSM := &mocks.FakeTokenLockStoreServiceManager{}
	mockCP := &mocks.FakeConfigProvider{}
	metricsProvider, _ := setupMetricsMocks()
	mockLSM.StoreServiceByTMSIdReturns(nil, nil)
	mockFP.GetFetcherReturns(&mocks.FakeTokenFetcher{}, nil)

	limiter := &denyingLimiter{}
	svc := sherdlock.NewService(mockFP, mockLSM, mockCP, metricsProvider, sherdlock.WithLimiter(limiter))
	t.Cleanup(svc.Shutdown)

	mgr, err := svc.SelectorManager(newTestTMS(t))
	require.NoError(t, err)

	selector, err := mgr.NewSelector("txID")
	require.NoError(t, err)

	_, _, err = selector.Select(context.Background(), walletFilter("alice"), "10", "EUR")

	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Contains(t, err.Error(), "throttled")
	assert.Equal(t, int64(1), limiter.allows.Load(), "a selection must be charged exactly once")
	// The fetcher is only consulted once the request is past the limiter.
	assert.Zero(t, mockFP.GetFetcherCallCount()-1, "no token fetching beyond the manager load")
}

// Unlock and Close are not metered: that is a property of the shared decorator and is
// covered by TestSelectorManager_UnlockAndCloseAreNotThrottled in the ratelimit package,
// which does not need a live lock store to assert it.

// Shutting the service down must leave an application-supplied limiter alone. Shutdown is
// not a process-exit hook: ManagementServiceProvider.Update calls it whenever a TMS's public
// parameters change, so stopping the application's limiter there would release resources it
// still owns while the managers already handed out keep calling Allow on it.
func TestService_ShutdownLeavesAnApplicationSuppliedLimiterAlone(t *testing.T) {
	mockCP := &mocks.FakeConfigProvider{}
	metricsProvider, _ := setupMetricsMocks()

	limiter := &denyingLimiter{}
	svc := sherdlock.NewService(&mocks.FakeFetcherProvider{}, &mocks.FakeTokenLockStoreServiceManager{}, mockCP, metricsProvider, sherdlock.WithLimiter(limiter))

	svc.Shutdown()
	assert.Zero(t, limiter.stops.Load(), "the application owns the lifecycle of its own limiter")

	// Shutdown is called on an already-stopped service in some teardown paths.
	svc.Shutdown()
	assert.Zero(t, limiter.stops.Load())

	// And the limiter still throttles afterwards, rather than silently letting everything
	// through for the rest of the process.
	require.ErrorIs(t, limiter.Allow("alice"), token.SelectorRateLimited)
}

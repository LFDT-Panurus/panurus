/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/LFDT-Panurus/panurus/token/services/utils/types/transaction"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceUnit(t *testing.T) {
	mockFP := &mocks.FakeFetcherProvider{}
	mockLSM := &mocks.FakeTokenLockStoreServiceManager{}
	mockCP := &mocks.FakeConfigProvider{}
	metricsProvider, _ := setupMetricsMocks()

	svc := sherdlock.NewService(mockFP, mockLSM, mockCP, metricsProvider)
	require.NotNil(t, svc)

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
	})
}

// Minimal VaultProvider mock for NewManagementService
type tokenMockVP struct{}

func (v *tokenMockVP) Vault(network, channel, namespace string) (driver.Vault, error) {
	return &drivermock.Vault{}, nil
}

// TestServicePrecisionFollowsPublicParamsUpdate checks that the selectors use the precision of
// the current public parameters. After an update the TMS is rebuilt, but the service hands back
// the manager it cached for the TMS id when the parameters had a lower precision.
func TestServicePrecisionFollowsPublicParamsUpdate(t *testing.T) {
	fetcherProvider := &mocks.FakeFetcherProvider{}
	fetcherProvider.GetFetcherReturns(newFetcherOf("4294967296"), nil)
	lockStoreManager := &mocks.FakeTokenLockStoreServiceManager{}
	lockStoreManager.StoreServiceByTMSIdReturns(&tokenlockdb.StoreService{TokenLockStore: acceptingLockStore{}}, nil)
	metricsProvider, _ := setupMetricsMocks()
	svc := sherdlock.NewService(fetcherProvider, lockStoreManager, &mocks.FakeConfigProvider{}, metricsProvider)

	before, err := svc.SelectorManager(newTMSWithPrecision(t, 32))
	require.NoError(t, err)
	mgr, ok := before.(*sherdlock.Manager)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, mgr.Stop()) })

	after, err := svc.SelectorManager(newTMSWithPrecision(t, 64))
	require.NoError(t, err)
	assert.Same(t, before, after, "the manager is cached by TMS id")

	selector, err := after.NewSelector("tx1")
	require.NoError(t, err)
	// 2^32 does not fit in 32 bits, but it is a valid amount under the new 64-bit parameters.
	tokens, sum, err := selector.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "4294967296", "ABC")
	require.NoError(t, err)
	assert.Len(t, tokens, 1)
	assert.Equal(t, "4294967296", sum.Decimal())
}

// newTMSWithPrecision returns a management service for the same TMS id every time, whose public
// parameters have the given precision.
func newTMSWithPrecision(t *testing.T, precision uint64) *token.ManagementService {
	t.Helper()

	driverTMS := &drivermock.TokenManagerService{}
	ppm := &drivermock.PublicParamsManager{}
	pp := &drivermock.PublicParameters{}
	pp.PrecisionReturns(precision)
	ppm.PublicParametersReturns(pp)
	driverTMS.PublicParamsManagerReturns(ppm)

	tms, err := token.NewManagementService(
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"},
		driverTMS,
		nil,
		&tokenMockVP{},
		nil,
		nil,
	)
	require.NoError(t, err)

	return tms
}

// acceptingLockStore grants every lock. A selection only locks and unlocks tokens.
type acceptingLockStore struct {
	dbdriver.TokenLockStore
}

func (acceptingLockStore) Lock(context.Context, *token2.ID, transaction.ID, string) error {
	return nil
}

func (acceptingLockStore) UnlockByTxID(context.Context, transaction.ID) error {
	return nil
}

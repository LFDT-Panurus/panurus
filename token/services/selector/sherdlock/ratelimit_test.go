/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock/mocks"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFetcherOf returns a fetcher that hands out a fresh iterator over a single token of the given
// quantity on every call, so repeated selections all see the same funds.
func newFetcherOf(quantity string) *mocks.FakeTokenFetcher {
	fetcher := &mocks.FakeTokenFetcher{}
	fetcher.UnspentTokensIteratorByStub = func(context.Context, string, token2.Type, int) (sherdlock.Iterator[*token2.UnspentTokenInWallet], error) {
		it := &mocks.FakeIterator[*token2.UnspentTokenInWallet]{}
		it.NextReturnsOnCall(0, &token2.UnspentTokenInWallet{
			Id:       token2.ID{TxId: "tx1", Index: 0},
			Type:     "ABC",
			Quantity: quantity,
		}, nil)
		it.NextReturnsOnCall(1, nil, nil)

		return it, nil
	}

	return fetcher
}

// TestRateLimitedManager verifies that the built-in limiter, wrapped around a real sherdlock
// manager, throttles per wallet without leaking locks: the denied selection never reaches the
// locker, so there is nothing left locked, and releasing tokens keeps working while throttled.
func TestRateLimitedManager(t *testing.T) {
	_, metrics := setupMetricsMocks()
	fetcher := newFetcherOf("100")
	locker := &mocks.FakeLocker{}
	ctx := t.Context()

	mgr := sherdlock.NewManager(&sherdlock.Config{
		Fetcher:                fetcher,
		Locker:                 locker,
		Precision:              64,
		MaxTokensPerSelection:  10000,
		MaxLockAttempts:        50000,
		MaxRetriesAfterBackOff: 3,
		SelectionTimeout:       30 * time.Second,
		Metrics:                metrics,
	})
	t.Cleanup(func() { require.NoError(t, mgr.Stop()) })

	// A burst of one request, and a rate slow enough that nothing refills during the test.
	limited := ratelimit.Decorate(mgr, ratelimit.New(ratelimit.Config{Rate: 0.001, Burst: 1}), "net1,ch1,ns1")

	selector, err := limited.NewSelector("tx1")
	require.NoError(t, err)

	// The first selection spends the wallet's single request and locks its token.
	tokens, sum, err := selector.Select(ctx, &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.NoError(t, err)
	assert.Len(t, tokens, 1)
	assert.Equal(t, "100", sum.Decimal())
	locksSoFar := locker.LockCallCount()
	assert.Equal(t, 1, locksSoFar)

	// The second one is denied before any token is even looked at.
	tokens, sum, err = selector.Select(ctx, &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Nil(t, tokens)
	assert.Nil(t, sum)
	assert.Equal(t, locksSoFar, locker.LockCallCount(), "a denied selection must not lock anything")
	assert.Equal(t, 0, locker.UnlockByTxIDCallCount(), "a denied selection has nothing to release")

	// Another wallet is unaffected.
	_, _, err = selector.Select(ctx, &unitTestMockOwnerFilter{id: "bob"}, "50", "ABC")
	require.NoError(t, err)

	// Releasing the tokens of a throttled wallet must always be possible.
	require.NoError(t, limited.Unlock(ctx, "tx1"))
	assert.Equal(t, 1, locker.UnlockByTxIDCallCount())
	require.NoError(t, limited.Close("tx1"))
}

// TestServiceRateLimiting verifies the service wiring: no rate limiting by default, and a limiter
// installed through the options that throttles the managers the service hands out.
func TestServiceRateLimiting(t *testing.T) {
	t.Run("DisabledByDefault", func(t *testing.T) {
		svc, tms := newServiceUnderTest(t)

		mgr, err := svc.SelectorManager(tms)
		require.NoError(t, err)
		// Without configuration or options the manager is handed out undecorated.
		assert.IsType(t, &sherdlock.Manager{}, mgr)
	})

	t.Run("WithLimiter", func(t *testing.T) {
		limiter := ratelimit.New(ratelimit.Config{Rate: 0.001, Burst: 1})
		svc, tms := newServiceUnderTest(t, ratelimit.WithLimiter(limiter))

		mgr, err := svc.SelectorManager(tms)
		require.NoError(t, err)
		assert.NotEqual(t, "*sherdlock.Manager", fmt.Sprintf("%T", mgr), "the manager must be decorated by the limiter")

		selector, err := mgr.NewSelector("tx1")
		require.NoError(t, err)

		// Spend the wallet's single request, then check the denial comes from the limiter,
		// before the selector touches the (absent) lock store.
		require.NoError(t, limiter.Allow(t.Context(), tms.ID().String(), "alice"))
		_, _, err = selector.Select(t.Context(), &unitTestMockOwnerFilter{id: "alice"}, "50", "ABC")
		require.ErrorIs(t, err, token.SelectorRateLimited)
	})

	t.Run("WithNilLimiter", func(t *testing.T) {
		svc, tms := newServiceUnderTest(t, ratelimit.WithLimiter(nil))

		mgr, err := svc.SelectorManager(tms)
		require.NoError(t, err)
		assert.IsType(t, &sherdlock.Manager{}, mgr)
	})

	t.Run("ShutdownKeepsTheLimiter", func(t *testing.T) {
		limiter := ratelimit.New(ratelimit.Config{Rate: 0.001, Burst: 1})
		svc, tms := newServiceUnderTest(t, ratelimit.WithLimiter(limiter))

		_, err := svc.SelectorManager(tms)
		require.NoError(t, err)
		require.NoError(t, limiter.Allow(t.Context(), tms.ID().String(), "alice"))

		// Shutdown also runs on public-parameter reloads, so it must not hand the wallet a
		// fresh allowance.
		svc.Shutdown()
		require.ErrorIs(t, limiter.Allow(t.Context(), tms.ID().String(), "alice"), token.SelectorRateLimited)
	})
}

// newServiceUnderTest returns a sherdlock service backed by mocks, together with the management
// service to ask it for a manager.
func newServiceUnderTest(t *testing.T, opts ...ratelimit.Option) (*sherdlock.SelectorService, *token.ManagementService) {
	t.Helper()

	fetcherProvider := &mocks.FakeFetcherProvider{}
	fetcherProvider.GetFetcherReturns(newFetcherOf("100"), nil)
	lockStoreManager := &mocks.FakeTokenLockStoreServiceManager{}
	lockStoreManager.StoreServiceByTMSIdReturns(nil, nil)
	metricsProvider, _ := setupMetricsMocks()

	svc := sherdlock.NewService(fetcherProvider, lockStoreManager, &mocks.FakeConfigProvider{}, metricsProvider, opts...)
	t.Cleanup(svc.Shutdown)

	driverTMS := &drivermock.TokenManagerService{}
	ppm := &drivermock.PublicParamsManager{}
	pp := &drivermock.PublicParameters{}
	pp.PrecisionReturns(64)
	ppm.PublicParametersReturns(pp)
	driverTMS.PublicParamsManagerReturns(ppm)

	tms, err := token.NewManagementService(
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"},
		driverTMS,
		nil,
		&rateLimitMockVaultProvider{},
		nil,
		nil,
	)
	require.NoError(t, err)

	return svc, tms
}

// rateLimitMockVaultProvider is the minimal VaultProvider NewManagementService needs.
type rateLimitMockVaultProvider struct{}

func (v *rateLimitMockVaultProvider) Vault(network, channel, namespace string) (driver.Vault, error) {
	return &drivermock.Vault{}, nil
}

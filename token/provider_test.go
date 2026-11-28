/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func (m *mockTokenManagerServiceProvider) ConfigurationFor(network, channel, namespace string) (driver.Configuration, error) {
	return nil, errors.Errorf("not implemented")
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
	provider.services[TMSID{Network: "net1", Channel: "ch1", Namespace: "ns1"}] = &ManagementService{}

	tmsID := TMSID{
		Network:   "net1",
		Channel:   "ch1",
		Namespace: "ns1",
	}

	err := provider.Update(tmsID, []byte("new params"))

	require.NoError(t, err)
	assert.Equal(t, 1, tmsProvider.updateCallCount)

	// Verify cache was cleared
	_, exists := provider.services[TMSID{Network: "net1", Channel: "ch1", Namespace: "ns1"}]
	assert.False(t, exists)
}

// TestManagementServiceProvider_GetManagementService_TMSNotFound verifies that ErrTMSNotFound
// remains distinguishable via errors.Is through GetManagementService's wrap chain.
func TestManagementServiceProvider_GetManagementService_TMSNotFound(t *testing.T) {
	tmsProvider := &mockTokenManagerServiceProvider{
		getTMSErr: errors.Join(errors.New("cannot retrieve public params"), ErrTMSNotFound),
	}
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

	_, err := provider.GetManagementService(WithTMSID(TMSID{
		Network:   "net1",
		Channel:   "ch1",
		Namespace: "ns1",
	}))

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTMSNotFound))
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

// TestManagementServiceProvider_KeysDoNotCollide verifies that a TMS does not get the cached management
// service of another TMS whose concatenated coordinates are equal (#2851).
func TestManagementServiceProvider_KeysDoNotCollide(t *testing.T) {
	tmsProvider := &mockTokenManagerServiceProvider{getTMSErr: errors.New("not cached")}
	provider := NewManagementServiceProvider(
		tmsProvider,
		&mockNormalizer{},
		&mockVaultProvider{},
		&mockCertificationClientProvider{},
		&mockSelectorManagerProvider{},
	)
	provider.services[TMSID{Network: "a", Channel: "b", Namespace: "c"}] = &ManagementService{}

	ms, err := provider.GetManagementService(WithTMSID(TMSID{Network: "ab", Channel: "", Namespace: "c"}))

	require.ErrorContains(t, err, "not cached")
	assert.Nil(t, ms)
}

// hookTMSProvider is a concurrency-safe driver.TokenManagerServiceProvider whose behaviour is set per test.
type hookTMSProvider struct {
	get    func(opts driver.ServiceOptions) (driver.TokenManagerService, error)
	update func(opts driver.ServiceOptions) error
}

func (h *hookTMSProvider) GetTokenManagerService(opts driver.ServiceOptions) (driver.TokenManagerService, error) {
	return h.get(opts)
}

func (h *hookTMSProvider) Update(opts driver.ServiceOptions) error {
	return h.update(opts)
}

func (h *hookTMSProvider) ConfigurationFor(network, channel, namespace string) (driver.Configuration, error) {
	return nil, errors.Errorf("not implemented")
}

// newHookProvider returns a ManagementServiceProvider over h whose management services can be built.
func newHookProvider(h *hookTMSProvider) *ManagementServiceProvider {
	return NewManagementServiceProvider(
		h,
		&mockNormalizer{},
		&mockVaultProvider{vault: &mock.Vault{}},
		&mockCertificationClientProvider{},
		&mockSelectorManagerProvider{},
	)
}

// newBuildableTMS returns a mock TMS that NewManagementService accepts.
func newBuildableTMS() *mock.TokenManagerService {
	tms := &mock.TokenManagerService{}
	tms.PublicParamsManagerReturns(&mock.PublicParamsManager{})

	return tms
}

// TestManagementServiceProvider_HungTMSDoesNotBlockOtherTMSs verifies that a TMS whose creation hangs
// blocks neither the creation nor the update of a different TMS (#2306).
func TestManagementServiceProvider_HungTMSDoesNotBlockOtherTMSs(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := &hookTMSProvider{
		get: func(opts driver.ServiceOptions) (driver.TokenManagerService, error) {
			if opts.Namespace == "ns1" {
				close(entered)
				<-release
			}

			return newBuildableTMS(), nil
		},
		update: func(driver.ServiceOptions) error { return nil },
	}
	p := newHookProvider(h)

	hung := make(chan error, 1)
	go func() {
		_, err := p.GetManagementService(WithTMSID(TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}))
		hung <- err
	}()
	<-entered
	defer func() {
		close(release)
		require.NoError(t, <-hung)
	}()

	done := make(chan error, 2)
	go func() {
		_, err := p.GetManagementService(WithTMSID(TMSID{Network: "n1", Channel: "c1", Namespace: "ns2"}))
		done <- err
	}()
	go func() {
		done <- p.Update(TMSID{Network: "n1", Channel: "c1", Namespace: "ns3"}, []byte("pp"))
	}()
	for range 2 {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("a call for another TMS is blocked behind the hung creation of n1/c1/ns1")
		}
	}
}

// waitForLockWaiter waits until a goroutine running fn is blocked on a sync mutex.
func waitForLockWaiter(t *testing.T, fn string) {
	t.Helper()
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		for g := range strings.SplitSeq(string(buf), "\n\n") {
			if strings.Contains(g, fn) && strings.Contains(g, "[sync.") {
				return true
			}
		}

		return false
	}, 5*time.Second, time.Millisecond)
}

// TestManagementServiceProvider_ConcurrentGetBuildsOnce verifies that a caller waiting behind the
// creation of the same TMS returns the service built by the first caller instead of building another one.
func TestManagementServiceProvider_ConcurrentGetBuildsOnce(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h := &hookTMSProvider{
		get: func(driver.ServiceOptions) (driver.TokenManagerService, error) {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
			}

			return newBuildableTMS(), nil
		},
	}
	p := newHookProvider(h)
	id := WithTMSID(TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"})

	results := make(chan *ManagementService, 2)
	errs := make(chan error, 2)
	get := func() {
		ms, err := p.GetManagementService(id)
		results <- ms
		errs <- err
	}
	go get()
	<-entered
	go get()
	waitForLockWaiter(t, "managementService")
	close(release)

	first, second := <-results, <-results
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	assert.Same(t, first, second)
	assert.Equal(t, int32(1), calls.Load())
}

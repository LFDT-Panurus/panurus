/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package driver_test

import (
	"sync"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core"
	cdmock "github.com/LFDT-Panurus/panurus/token/core/common/driver/mock"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	dmock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	imock "github.com/LFDT-Panurus/panurus/token/services/identity/driver/mock"
	idmock "github.com/LFDT-Panurus/panurus/token/services/identity/mock"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/observability"
	"github.com/LFDT-Panurus/panurus/token/services/storage"
	zkatsnarkdriver "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/driver"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

var (
	wiringConfigurationOnce sync.Once
	wiringConfigurationVal  *testutils.SetupConfiguration
)

// wiringConfiguration returns a BN254 setup configuration shared by the
// driver wiring tests, so the circuit setup runs once per test binary.
func wiringConfiguration(t *testing.T) *testutils.SetupConfiguration {
	t.Helper()
	wiringConfigurationOnce.Do(func() {
		wiringConfigurationVal = upgradeConfiguration(t, 0)
	})
	require.NotNil(t, wiringConfigurationVal)

	return wiringConfigurationVal
}

// failingLimitsProvider is a ResourceLimitsProvider that always fails.
type failingLimitsProvider struct{}

func (failingLimitsProvider) ResourceLimits() (tdriver.ResourceLimits, error) {
	return tdriver.ResourceLimits{}, errors.New("limits-error")
}

// driverFixture bundles the mocks behind a zkatsnark Driver.
type driverFixture struct {
	configService    *cdmock.ConfigService
	storageProvider  *imock.StorageProvider
	identityProvider *cdmock.IdentityProvider
	networkProvider  *cdmock.NetworkProvider
	vaultProvider    *cdmock.VaultProvider
	driver           *zkatsnarkdriver.Driver
	name             core.TokenDriverIdentifier
}

func newDriverFixture(t *testing.T, limits tdriver.ResourceLimitsProvider) *driverFixture {
	t.Helper()
	f := &driverFixture{
		configService:    &cdmock.ConfigService{},
		storageProvider:  &imock.StorageProvider{},
		identityProvider: &cdmock.IdentityProvider{},
		networkProvider:  &cdmock.NetworkProvider{},
		vaultProvider:    &cdmock.VaultProvider{},
	}
	factory := zkatsnarkdriver.NewTokenDriver(
		&disabled.Provider{},
		noop.NewTracerProvider(),
		f.configService,
		f.storageProvider,
		f.identityProvider,
		&idmock.NetworkBinderService{},
		f.networkProvider,
		f.vaultProvider,
		limits,
	)
	d, ok := factory.Driver.(*zkatsnarkdriver.Driver)
	require.True(t, ok)
	f.driver = d
	f.name = factory.Name

	return f
}

// wireSuccess configures the mocks so that NewTokenService can succeed.
func (f *driverFixture) wireSuccess(tmsID tdriver.TMSID) {
	nw := &cdmock.Network{}
	lm := &cdmock.LocalMembership{}
	lm.DefaultIdentityReturns([]byte("network-identity"))
	nw.LocalMembershipReturns(lm)
	f.networkProvider.GetNetworkReturns(network.NewNetwork(nw, network.NewLocalMembership(lm)), nil)
	vault := &dmock.Vault{}
	vault.QueryEngineReturns(&dmock.QueryEngine{})
	f.vaultProvider.VaultReturns(vault, nil)
	identityStore := &imock.IdentityStoreService{}
	identityStore.IteratorConfigurationsReturns(&cdmock.IdentityConfigurationIterator{}, nil)
	identityStore.NotifierReturns(nil, storage.ErrNotSupported)
	f.storageProvider.IdentityStoreReturns(identityStore, nil)
	f.storageProvider.KeystoreReturns(&cdmock.Keystore{}, nil)
	f.storageProvider.WalletStoreReturns(&imock.WalletStoreService{}, nil)
	f.identityProvider.DefaultIdentityReturns([]byte("fsc-identity"))
	config := &dmock.Configuration{}
	config.IDReturns(tmsID)
	f.configService.ConfigurationForReturns(config, nil)
}

// TestNewDriver checks the name of the zkatsnark driver factory.
func TestNewDriver(t *testing.T) {
	f := newDriverFixture(t, tdriver.StaticResourceLimits(tdriver.DefaultResourceLimits()))
	assert.NotNil(t, f.driver)
	assert.Equal(t, core.DriverIdentifier("zkatsnark", 1), f.name)
}

// TestNewTokenService_EarlyErrors covers the error paths of NewTokenService
// that fail before any public parameters preparation.
func TestNewTokenService_EarlyErrors(t *testing.T) {
	tmsID := tdriver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	f := newDriverFixture(t, tdriver.StaticResourceLimits(tdriver.DefaultResourceLimits()))

	ts, err := f.driver.NewTokenService(tmsID, nil)
	require.Error(t, err)
	assert.Nil(t, ts)
	assert.Contains(t, err.Error(), "empty public parameters")

	f.networkProvider.GetNetworkReturns(nil, errors.New("network-error"))
	ts, err = f.driver.NewTokenService(tmsID, []byte("pp"))
	require.Error(t, err)
	assert.Nil(t, ts)
	assert.Contains(t, err.Error(), "network-error")

	f.networkProvider.GetNetworkReturns(&network.Network{}, nil)
	f.vaultProvider.VaultReturns(nil, errors.New("vault-error"))
	ts, err = f.driver.NewTokenService(tmsID, []byte("pp"))
	require.Error(t, err)
	assert.Nil(t, ts)
	assert.Contains(t, err.Error(), "vault-error")

	f.vaultProvider.VaultReturns(&dmock.Vault{}, nil)
	f.configService.ConfigurationForReturns(nil, errors.New("config-error"))
	ts, err = f.driver.NewTokenService(tmsID, []byte("pp"))
	require.Error(t, err)
	assert.Nil(t, ts)
	assert.Contains(t, err.Error(), "failed to get config for token service")

	f.configService.ConfigurationForReturns(&dmock.Configuration{}, nil)
	ts, err = f.driver.NewTokenService(tmsID, []byte("invalid-pp"))
	require.Error(t, err)
	assert.Nil(t, ts)
	assert.Contains(t, err.Error(), "failed to initialize public params manager")
}

// TestNewTokenService_Wiring checks that NewTokenService passes the limits
// resolved from the injected ResourceLimitsProvider to the validator, wraps the
// wallet service with the observability decorator, and fails when the limits
// cannot be resolved.
func TestNewTokenService_Wiring(t *testing.T) {
	tmsID := tdriver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	ppBytes, err := wiringConfiguration(t).PP.Serialize()
	require.NoError(t, err)

	t.Run("configured limits", func(t *testing.T) {
		limits := tdriver.ResourceLimits{MaxOutputs: 7, MaxInputs: 5}.WithDefaults()
		f := newDriverFixture(t, tdriver.StaticResourceLimits(limits))
		f.wireSuccess(tmsID)

		ts, err := f.driver.NewTokenService(tmsID, ppBytes)
		require.NoError(t, err)
		require.NotNil(t, ts)

		v, err := ts.Validator()
		require.NoError(t, err)
		val, ok := v.(*validator.Validator)
		require.True(t, ok, "unexpected validator type [%T]", v)
		assert.Equal(t, limits, val.Limits)
		assert.Equal(t, 7, val.Limits.MaxOutputs)
		assert.Equal(t, 5, val.Limits.MaxInputs)

		assert.IsType(t, &observability.WalletServiceDecorator{}, ts.WalletService())
		assert.NotNil(t, ts.Authorization())
	})

	t.Run("provider error", func(t *testing.T) {
		f := newDriverFixture(t, failingLimitsProvider{})
		f.wireSuccess(tmsID)

		ts, err := f.driver.NewTokenService(tmsID, ppBytes)
		require.Error(t, err)
		assert.Nil(t, ts)
		assert.Contains(t, err.Error(), "failed resolving validator resource limits")
		assert.Contains(t, err.Error(), "limits-error")
	})

	t.Run("wallet service error", func(t *testing.T) {
		f := newDriverFixture(t, tdriver.StaticResourceLimits(tdriver.DefaultResourceLimits()))
		f.wireSuccess(tmsID)
		f.storageProvider.IdentityStoreReturns(nil, errors.New("identity-store-error"))

		ts, err := f.driver.NewTokenService(tmsID, ppBytes)
		require.Error(t, err)
		assert.Nil(t, ts)
		assert.Contains(t, err.Error(), "failed to initialize wallet service")
	})
}

// TestWalletServiceFactory checks that the standalone wallet service factory
// returns a wallet service decorated with the observability circuit breaker
// and propagates the errors of the underlying constructor.
func TestWalletServiceFactory(t *testing.T) {
	tmsID := tdriver.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"}
	config := &dmock.Configuration{}
	config.IDReturns(tmsID)

	t.Run("decorated", func(t *testing.T) {
		storageProvider := &imock.StorageProvider{}
		identityStore := &imock.IdentityStoreService{}
		identityStore.IteratorConfigurationsReturns(&cdmock.IdentityConfigurationIterator{}, nil)
		identityStore.NotifierReturns(nil, storage.ErrNotSupported)
		storageProvider.IdentityStoreReturns(identityStore, nil)
		storageProvider.KeystoreReturns(&cdmock.Keystore{}, nil)
		storageProvider.WalletStoreReturns(&imock.WalletStoreService{}, nil)

		ws, err := zkatsnarkdriver.NewWalletServiceFactory(storageProvider).Driver.NewWalletService(config, wiringConfiguration(t).PP)
		require.NoError(t, err)
		assert.IsType(t, &observability.WalletServiceDecorator{}, ws)
	})

	t.Run("invalid public parameters type", func(t *testing.T) {
		ws, err := zkatsnarkdriver.NewWalletServiceFactory(&imock.StorageProvider{}).Driver.NewWalletService(config, &dmock.PublicParameters{})
		require.Error(t, err)
		assert.Nil(t, ws)
		assert.Contains(t, err.Error(), "invalid public parameters type")
	})

	t.Run("identity store error", func(t *testing.T) {
		storageProvider := &imock.StorageProvider{}
		storageProvider.IdentityStoreReturns(nil, errors.New("identity-store-error"))

		ws, err := zkatsnarkdriver.NewWalletServiceFactory(storageProvider).Driver.NewWalletService(config, wiringConfiguration(t).PP)
		require.Error(t, err)
		assert.Nil(t, ws)
		assert.Contains(t, err.Error(), "identity-store-error")
	})
}

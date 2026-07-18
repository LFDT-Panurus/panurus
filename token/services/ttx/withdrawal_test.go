/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This white-box (package ttx) file covers getRecipientIdentity, which is
// unexported. It is kept separate from black-box tests so it can avoid
// importing dep/mock, which cannot be imported from package ttx without
// creating an import cycle (see cleanupsessions_test.go).
package ttx

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driver_mock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	token_mock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// fakeTMSProvider is a minimal driver.TokenManagerServiceProvider that always
// returns the wrapped, pre-stubbed driver.TokenManagerService.
type fakeTMSProvider struct {
	tms driver.TokenManagerService
}

func (f *fakeTMSProvider) ConfigurationFor(network, channel, namespace string) (driver.Configuration, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTMSProvider) GetTokenManagerService(_ driver.ServiceOptions) (driver.TokenManagerService, error) {
	return f.tms, nil
}

func (f *fakeTMSProvider) Update(_ driver.ServiceOptions) error { return nil }

// passthroughNormalizer is a minimal token.Normalizer that returns the
// options unchanged.
type passthroughNormalizer struct{}

func (passthroughNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	return opt, nil
}

// minimalViewContext is a hand-rolled minimal view.Context fake (mirroring
// the countingSession pattern in cleanupsessions_test.go) used because
// dep/mock.Context cannot be imported from this white-box package.
type minimalViewContext struct {
	svc any
	ctx context.Context
}

func (c *minimalViewContext) StartSpanFrom(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, nil
}
func (c *minimalViewContext) GetService(_ any) (any, error) { return c.svc, nil }
func (c *minimalViewContext) ID() string                    { return "test-context" }
func (c *minimalViewContext) RunView(_ view.View, _ ...view.RunViewOption) (any, error) {
	return nil, nil
}
func (c *minimalViewContext) Me() view.Identity         { return nil }
func (c *minimalViewContext) IsMe(_ view.Identity) bool { return false }
func (c *minimalViewContext) Initiator() view.View      { return nil }
func (c *minimalViewContext) GetSession(_ view.View, _ view.Identity, _ ...view.View) (view.Session, error) {
	return nil, nil
}
func (c *minimalViewContext) GetSessionByID(_ string, _ view.Identity) (view.Session, error) {
	return nil, nil
}
func (c *minimalViewContext) Session() view.Session { return nil }
func (c *minimalViewContext) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}

	return context.Background()
}
func (c *minimalViewContext) OnError(_ func()) {}

// newWithdrawalTestContext builds a minimalViewContext whose GetService call
// resolves a real *token.ManagementServiceProvider backed by mockTMS, so that
// token.GetManagementService (used by GetWallet) succeeds.
func newWithdrawalTestContext(t *testing.T, mockTMS *driver_mock.TokenManagerService) *minimalViewContext {
	t.Helper()

	mockVaultProvider := &token_mock.VaultProvider{}
	mockVaultProvider.VaultReturns(&driver_mock.Vault{}, nil)

	provider := token.NewManagementServiceProvider(
		&fakeTMSProvider{tms: mockTMS},
		passthroughNormalizer{},
		mockVaultProvider,
		nil,
		nil,
	)

	return &minimalViewContext{svc: provider}
}

// newStubbedTokenManagerService returns a driver.TokenManagerService with all
// the stubs required for ManagementService.init() to succeed, with the given
// wallet service.
func newStubbedTokenManagerService(walletService driver.WalletService) *driver_mock.TokenManagerService {
	mockTMS := &driver_mock.TokenManagerService{}
	mockTMS.WalletServiceReturns(walletService)
	mockTMS.ValidatorReturns(&driver_mock.Validator{}, nil)
	mockTMS.AuthorizationReturns(&driver_mock.Authorization{})
	mockTMS.ConfigurationReturns(&driver_mock.Configuration{})
	mockTMS.TokensServiceReturns(&driver_mock.TokensService{})
	mockTMS.TokensUpgradeServiceReturns(&driver_mock.TokensUpgradeService{})
	mockPPM := &driver_mock.PublicParamsManager{}
	mockPPM.PublicParametersReturns(&driver_mock.PublicParameters{})
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockTMS.DeserializerReturns(&driver_mock.Deserializer{})
	mockTMS.IdentityProviderReturns(&driver_mock.IdentityProvider{})
	mockTMS.CertificationServiceReturns(nil)

	return mockTMS
}

// TestGetRecipientIdentity_ExternalRecipientData_RegistersAndReturnsData verifies that when
// r.RecipientData is caller-supplied, getRecipientIdentity registers it against the local
// wallet before using it, marks the wallet as external, and returns a nil *token.OwnerWallet
// (so Call signs via the external signer, not the local wallet).
func TestGetRecipientIdentity_ExternalRecipientData_RegistersAndReturnsData(t *testing.T) {
	mockOwnerWallet := &driver_mock.OwnerWallet{}
	mockOwnerWallet.RegisterRecipientReturns(nil)

	mockWalletService := &driver_mock.WalletService{}
	mockWalletService.OwnerWalletReturns(mockOwnerWallet, nil)

	mockTMS := newStubbedTokenManagerService(mockWalletService)
	ctx := newWithdrawalTestContext(t, mockTMS)

	data := &RecipientData{
		Identity:  driver.Identity("recipient-identity"),
		AuditInfo: []byte("audit-info"),
	}
	r := &RequestWithdrawalView{
		Wallet:        "external-wallet",
		RecipientData: data,
	}

	tmsID, recipientData, w, err := r.getRecipientIdentity(ctx)

	require.NoError(t, err)
	assert.NotNil(t, tmsID)
	assert.Same(t, data, recipientData)
	assert.Nil(t, w)
	assert.True(t, r.ExternalWallet, "external wallet flag must be set once RecipientData is used")

	require.Equal(t, 1, mockOwnerWallet.RegisterRecipientCallCount())
	_, registered := mockOwnerWallet.RegisterRecipientArgsForCall(0)
	assert.Same(t, data, registered)
}

// TestGetRecipientIdentity_ExternalRecipientData_RegisterFails verifies that when the local
// wallet rejects the caller-supplied RecipientData (e.g. identity/audit-info mismatch),
// getRecipientIdentity returns an error and never proceeds with the withdrawal.
func TestGetRecipientIdentity_ExternalRecipientData_RegisterFails(t *testing.T) {
	mockOwnerWallet := &driver_mock.OwnerWallet{}
	mockOwnerWallet.RegisterRecipientReturns(errors.New("failed to match identity"))

	mockWalletService := &driver_mock.WalletService{}
	mockWalletService.OwnerWalletReturns(mockOwnerWallet, nil)

	mockTMS := newStubbedTokenManagerService(mockWalletService)
	ctx := newWithdrawalTestContext(t, mockTMS)

	r := &RequestWithdrawalView{
		Wallet: "external-wallet",
		RecipientData: &RecipientData{
			Identity:  driver.Identity("recipient-identity"),
			AuditInfo: []byte("mismatched-audit-info"),
		},
	}

	tmsID, recipientData, w, err := r.getRecipientIdentity(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to register recipient data")
	assert.Nil(t, tmsID)
	assert.Nil(t, recipientData)
	assert.Nil(t, w)
	assert.False(t, r.ExternalWallet, "external wallet flag must not be set on failure")
}

// TestGetRecipientIdentity_NoRecipientData_UsesLocalWallet verifies that when no
// RecipientData is supplied, getRecipientIdentity falls back to the local wallet's own
// GetRecipientData and returns the wallet itself, unchanged from prior behavior.
func TestGetRecipientIdentity_NoRecipientData_UsesLocalWallet(t *testing.T) {
	localRecipientData := &driver.RecipientData{
		Identity:  driver.Identity("local-identity"),
		AuditInfo: []byte("local-audit-info"),
	}

	mockOwnerWallet := &driver_mock.OwnerWallet{}
	mockOwnerWallet.GetRecipientDataReturns(localRecipientData, nil)

	mockWalletService := &driver_mock.WalletService{}
	mockWalletService.OwnerWalletReturns(mockOwnerWallet, nil)

	mockTMS := newStubbedTokenManagerService(mockWalletService)
	ctx := newWithdrawalTestContext(t, mockTMS)

	r := &RequestWithdrawalView{Wallet: "local-wallet"}

	tmsID, recipientData, w, err := r.getRecipientIdentity(ctx)

	require.NoError(t, err)
	assert.NotNil(t, tmsID)
	assert.Same(t, localRecipientData, recipientData)
	assert.NotNil(t, w)
	assert.False(t, r.ExternalWallet)
	assert.Equal(t, 0, mockOwnerWallet.RegisterRecipientCallCount())
}

// TestGetRecipientIdentity_GetRecipientDataFails verifies that when no
// RecipientData is supplied and the local wallet's own GetRecipientData call
// fails, getRecipientIdentity surfaces the error instead of proceeding.
func TestGetRecipientIdentity_GetRecipientDataFails(t *testing.T) {
	mockOwnerWallet := &driver_mock.OwnerWallet{}
	mockOwnerWallet.GetRecipientDataReturns(nil, errors.New("keystore unavailable"))

	mockWalletService := &driver_mock.WalletService{}
	mockWalletService.OwnerWalletReturns(mockOwnerWallet, nil)

	mockTMS := newStubbedTokenManagerService(mockWalletService)
	ctx := newWithdrawalTestContext(t, mockTMS)

	r := &RequestWithdrawalView{Wallet: "local-wallet"}

	tmsID, recipientData, w, err := r.getRecipientIdentity(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get recipient data")
	assert.Nil(t, tmsID)
	assert.Nil(t, recipientData)
	assert.Nil(t, w)
}

// TestGetRecipientIdentity_WalletNotFound verifies that when the requested wallet
// cannot be resolved (e.g. the wallet service fails to return an owner wallet),
// getRecipientIdentity surfaces a "wallet not found" error instead of panicking
// on a nil wallet.
func TestGetRecipientIdentity_WalletNotFound(t *testing.T) {
	mockWalletService := &driver_mock.WalletService{}
	mockWalletService.OwnerWalletReturns(nil, errors.New("no such wallet"))

	mockTMS := newStubbedTokenManagerService(mockWalletService)
	ctx := newWithdrawalTestContext(t, mockTMS)

	r := &RequestWithdrawalView{Wallet: "missing-wallet"}

	tmsID, recipientData, w, err := r.getRecipientIdentity(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Nil(t, tmsID)
	assert.Nil(t, recipientData)
	assert.Nil(t, w)
}

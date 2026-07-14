/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nfttx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	driver_mock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	token_mock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx"
	"github.com/LFDT-Panurus/panurus/token/services/nfttx/mock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTMSProvider struct{}

func (m *mockTMSProvider) GetManagementService(opts ...token.ServiceOption) (*token.ManagementService, error) {
	return &token.ManagementService{}, nil
}

func getFakeCtx() *mock.Context {
	fakeCtx := &mock.Context{}
	fakeCtx.GetServiceCalls(func(v any) (any, error) {
		if _, ok := v.(*token.ManagementServiceProvider); ok {
			return &mockTMSProvider{}, nil
		}

		return nil, nil
	})

	return fakeCtx
}

type dummyNormalizer struct{}

func (d *dummyNormalizer) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	return opt, nil
}

type dummyTMSProvider struct{}

func (d *dummyTMSProvider) GetTokenManagerService(opts driver.ServiceOptions) (driver.TokenManagerService, error) {
	return nil, errors.New("dummy tms provider error")
}
func (d *dummyTMSProvider) Update(opts driver.ServiceOptions) error {
	return nil
}

type dummyTMSProviderSuccess struct{}

func (d *dummyTMSProviderSuccess) GetTokenManagerService(opts driver.ServiceOptions) (driver.TokenManagerService, error) {
	mockTMS := &driver_mock.TokenManagerService{}
	mockTMS.WalletServiceReturns(&driver_mock.WalletService{})
	mockTMS.ValidatorReturns(&driver_mock.Validator{}, nil)
	mockAuth := &driver_mock.Authorization{}
	mockTMS.AuthorizationReturns(mockAuth)
	mockPPM := &driver_mock.PublicParamsManager{}
	mockPP := &driver_mock.PublicParameters{}
	mockPP.PrecisionReturns(64)
	mockPPM.PublicParametersReturns(mockPP)
	mockTMS.PublicParamsManagerReturns(mockPPM)

	return mockTMS, nil
}
func (d *dummyTMSProviderSuccess) Update(opts driver.ServiceOptions) error {
	return nil
}

func getErrorCtx() *mock.Context {
	fakeCtx := &mock.Context{}
	p := token.NewManagementServiceProvider(&dummyTMSProvider{}, &dummyNormalizer{}, nil, nil, nil)
	fakeCtx.GetServiceCalls(func(v any) (any, error) {
		if _, ok := v.(*token.ManagementServiceProvider); ok {
			return p, nil
		}

		return nil, nil
	})

	return fakeCtx
}

func getSuccessCtx() *mock.Context {
	fakeCtx := &mock.Context{}
	mockVP := &token_mock.VaultProvider{}
	mockVP.VaultReturns(&driver_mock.Vault{}, nil)

	p := token.NewManagementServiceProvider(&dummyTMSProviderSuccess{}, &dummyNormalizer{}, mockVP, nil, nil)
	fakeCtx.GetServiceCalls(func(v any) (any, error) {
		if _, ok := v.(*token.ManagementServiceProvider); ok {
			return p, nil
		}

		return nil, nil
	})

	return fakeCtx
}

func TestMyWallet(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.MyWallet(nil)
	})
	// This will panic inside because the WalletManager is nil
	assert.Panics(t, func() {
		nfttx.MyWallet(getFakeCtx())
	})
	assert.Nil(t, nfttx.MyWallet(getErrorCtx()))
	assert.NotNil(t, nfttx.MyWallet(getSuccessCtx()))
}

func TestMyWalletFromTx(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.MyWalletFromTx(nil, &nfttx.Transaction{Transaction: &ttx.Transaction{}})
	})
}

func TestGetWallet(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.GetWallet(nil, "my-owner")
	})
	assert.Panics(t, func() {
		nfttx.GetWallet(getFakeCtx(), "my-owner")
	})
	assert.Nil(t, nfttx.GetWallet(getErrorCtx(), "my-owner"))
	assert.NotNil(t, nfttx.GetWallet(getSuccessCtx(), "my-owner"))
}

func TestGetWalletForChannel(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.GetWalletForChannel(nil, "my-channel", "my-owner")
	})
	assert.Panics(t, func() {
		nfttx.GetWalletForChannel(getFakeCtx(), "my-channel", "my-owner")
	})
	assert.Nil(t, nfttx.GetWalletForChannel(getErrorCtx(), "my-channel", "my-owner"))
	assert.NotNil(t, nfttx.GetWalletForChannel(getSuccessCtx(), "my-channel", "my-owner"))
}

func TestMyIssuerWallet(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.MyIssuerWallet(nil)
	})
	assert.Panics(t, func() {
		nfttx.MyIssuerWallet(getFakeCtx())
	})
	assert.Nil(t, nfttx.MyIssuerWallet(getErrorCtx()))
	assert.NotNil(t, nfttx.MyIssuerWallet(getSuccessCtx()))
}

func TestGetIssuerWallet(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.GetIssuerWallet(nil, "issuer-id")
	})
	assert.Panics(t, func() {
		nfttx.GetIssuerWallet(getFakeCtx(), "issuer-id")
	})
	assert.Nil(t, nfttx.GetIssuerWallet(getErrorCtx(), "issuer-id"))
	assert.NotNil(t, nfttx.GetIssuerWallet(getSuccessCtx(), "issuer-id"))
}

func TestGetIssuerWalletForChannel(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.GetIssuerWalletForChannel(nil, "my-channel", "issuer-id")
	})
	assert.Panics(t, func() {
		nfttx.GetIssuerWalletForChannel(getFakeCtx(), "my-channel", "issuer-id")
	})
	assert.Nil(t, nfttx.GetIssuerWalletForChannel(getErrorCtx(), "my-channel", "issuer-id"))
	assert.NotNil(t, nfttx.GetIssuerWalletForChannel(getSuccessCtx(), "my-channel", "issuer-id"))
}

func TestMyAuditorWallet(t *testing.T) {
	assert.Panics(t, func() {
		nfttx.MyAuditorWallet(nil)
	})
	assert.Panics(t, func() {
		nfttx.MyAuditorWallet(getFakeCtx())
	})
	assert.Nil(t, nfttx.MyAuditorWallet(getErrorCtx()))
	assert.NotNil(t, nfttx.MyAuditorWallet(getSuccessCtx()))
}

func TestWithType(t *testing.T) {
	opts := &token.ListTokensOptions{}
	optFunc := nfttx.WithType(token2.Type("my-type"))
	err := optFunc(opts)
	require.NoError(t, err)
	assert.Equal(t, token2.Type("my-type"), opts.TokenType)
}

func TestOwnerWallet_QueryByKey(t *testing.T) {
	ow := &nfttx.OwnerWallet{
		ServiceProvider: nil,
		Precision:       64,
	}
	assert.Panics(t, func() {
		_ = ow.QueryByKey(context.TODO(), &House{}, "LinearID", "123")
	})
}

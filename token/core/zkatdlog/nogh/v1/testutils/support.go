/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package testutils

import (
	"context"
	"math/big"

	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/benchmark"
	v1setup "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// testPublicParamsManager is a simple implementation of PublicParametersManager for testing
type testPublicParamsManager struct {
	pp *v1setup.PublicParams
}

func (t *testPublicParamsManager) PublicParams() *v1setup.PublicParams {
	return t.pp
}

func (t *testPublicParamsManager) PublicParameters() driver.PublicParameters {
	return t.pp
}

func (t *testPublicParamsManager) NewCertifierKeyPair() ([]byte, []byte, error) {
	return nil, nil, errors.New("not implemented")
}

func (t *testPublicParamsManager) PublicParamsHash() driver.PPHash {
	return driver.PPHash{}
}

// testWalletService is a minimal mock WalletService for testing
type testWalletService struct {
	issuerSigner *benchmark.Signer
	auditInfoMap map[string][]byte
}

func (t *testWalletService) RegisterRecipientIdentity(ctx context.Context, data *driver.RecipientData) error {
	return errors.New("not implemented")
}

func (t *testWalletService) GetAuditInfo(ctx context.Context, id driver.Identity) ([]byte, error) {
	// For testing purposes, return audit info for any identity
	// First try exact match
	auditInfoRaw, ok := t.auditInfoMap[id.String()]
	if ok {
		return auditInfoRaw, nil
	}
	// If not found, return the first available audit info (for test simplicity)
	for _, info := range t.auditInfoMap {
		return info, nil
	}

	return nil, errors.New("audit info not found")
}

func (t *testWalletService) GetEnrollmentID(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	return "", errors.New("not implemented")
}

func (t *testWalletService) GetRevocationHandle(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, error) {
	return "", errors.New("not implemented")
}

func (t *testWalletService) GetEIDAndRH(ctx context.Context, identity driver.Identity, auditInfo []byte) (string, string, error) {
	return "", "", errors.New("not implemented")
}

func (t *testWalletService) Wallet(ctx context.Context, identity driver.Identity) driver.Wallet {
	return nil
}

func (t *testWalletService) RegisterOwnerIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	return errors.New("not implemented")
}

func (t *testWalletService) RegisterIssuerIdentity(ctx context.Context, config driver.IdentityConfiguration) error {
	return errors.New("not implemented")
}

func (t *testWalletService) OwnerWalletIDs(ctx context.Context) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (t *testWalletService) OwnerWallet(ctx context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
	return nil, errors.New("not implemented")
}

func (t *testWalletService) IssuerWallet(ctx context.Context, id driver.WalletLookupID) (driver.IssuerWallet, error) {
	return &testIssuerWallet{signer: t.issuerSigner}, nil
}

func (t *testWalletService) AuditorWallet(ctx context.Context, id driver.WalletLookupID) (driver.AuditorWallet, error) {
	return nil, errors.New("not implemented")
}

func (t *testWalletService) CertifierWallet(ctx context.Context, id driver.WalletLookupID) (driver.CertifierWallet, error) {
	return nil, errors.New("not implemented")
}

func (t *testWalletService) SpendIDs(ids ...*token2.ID) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (t *testWalletService) Done() error {
	return nil
}

// testIssuerWallet is a minimal mock IssuerWallet for testing
type testIssuerWallet struct {
	signer *benchmark.Signer
}

func (t *testIssuerWallet) ID() string {
	return "test-issuer-wallet"
}

func (t *testIssuerWallet) Contains(ctx context.Context, identity driver.Identity) bool {
	return true
}

func (t *testIssuerWallet) ContainsToken(ctx context.Context, token *token2.UnspentToken) bool {
	return false
}

func (t *testIssuerWallet) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	return t.signer, nil
}

func (t *testIssuerWallet) GetIssuerIdentity(tokenType token2.Type) (driver.Identity, error) {
	return nil, errors.New("not implemented")
}

func (t *testIssuerWallet) HistoryTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token2.IssuedTokens, error) {
	return nil, errors.New("not implemented")
}

func (t *testIssuerWallet) IssuedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	return nil, errors.New("not implemented")
}

func (t *testIssuerWallet) RedeemedBalance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	return nil, errors.New("not implemented")
}

func (t *testIssuerWallet) Balance(ctx context.Context, opts *driver.IssuerBalanceOptions) (*big.Int, error) {
	return nil, errors.New("not implemented")
}

// testIdentityProvider is a minimal mock IdentityProvider for testing
type testIdentityProvider struct {
	ownerSigner driver.SigningIdentity
}

func (t *testIdentityProvider) GetSigner(ctx context.Context, id driver.Identity) (driver.Signer, error) {
	return t.ownerSigner, nil
}

// testTokenLoader is a minimal mock TokenLoader for testing
type testTokenLoader struct {
	tokens map[string]v1.LoadedToken
}

func (t *testTokenLoader) LoadTokens(ctx context.Context, ids []*token2.ID) ([]v1.LoadedToken, error) {
	result := make([]v1.LoadedToken, len(ids))
	for i, id := range ids {
		key := id.String()
		tok, ok := t.tokens[key]
		if !ok {
			return nil, errors.Errorf("token not found: %s", key)
		}
		result[i] = tok
	}

	return result, nil
}

// testOwnerWallet is a minimal mock OwnerWallet for testing
type testOwnerWallet struct {
	id     string
	signer driver.SigningIdentity
}

func (t *testOwnerWallet) ID() string {
	return t.id
}

func (t *testOwnerWallet) Contains(ctx context.Context, identity driver.Identity) bool {
	return true
}

func (t *testOwnerWallet) ContainsToken(ctx context.Context, token *token2.UnspentToken) bool {
	return true
}

func (t *testOwnerWallet) GetSigner(ctx context.Context, identity driver.Identity) (driver.Signer, error) {
	return t.signer, nil
}

func (t *testOwnerWallet) GetRecipientIdentity(ctx context.Context) (driver.Identity, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) GetRecipientData(ctx context.Context) (*driver.RecipientData, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) GetAuditInfo(ctx context.Context, id driver.Identity) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) GetTokenMetadata(id driver.Identity) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) GetTokenMetadataAuditInfo(id driver.Identity) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) EnrollmentID() string {
	return ""
}

func (t *testOwnerWallet) RegisterRecipient(ctx context.Context, data *driver.RecipientData) error {
	return errors.New("not implemented")
}

func (t *testOwnerWallet) ListTokens(ctx context.Context, opts *driver.ListTokensOptions) (*token2.UnspentTokens, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) ListTokensIterator(ctx context.Context, opts *driver.ListTokensOptions) (driver.UnspentTokensIterator, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) Balance(ctx context.Context, opts *driver.ListTokensOptions) (*big.Int, error) {
	return nil, errors.New("not implemented")
}

func (t *testOwnerWallet) Remote() bool {
	return false
}

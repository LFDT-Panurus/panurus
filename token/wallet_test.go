/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package token tests wallet.go which provides token wallet management for owners, issuers, auditors, and certifiers.
package token

import (
	"context"
	"math/big"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithType verifies WithType option sets token type
func TestWithType(t *testing.T) {
	opts := &ListTokensOptions{}
	err := WithType("USD")(opts)

	require.NoError(t, err)
	assert.Equal(t, token.Type("USD"), opts.TokenType)
}

// TestWalletManager_RegisterOwnerIdentity verifies owner identity registration
func TestWalletManager_RegisterOwnerIdentity(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.RegisterOwnerIdentityReturns(nil)

	ctx := context.Background()
	err := wm.RegisterOwnerIdentity(ctx, "alice", "/path/to/alice")

	require.NoError(t, err)
	assert.Equal(t, 1, mockWS.RegisterOwnerIdentityCallCount())
}

// TestWalletManager_RegisterOwnerIdentityConfiguration verifies owner identity config registration
func TestWalletManager_RegisterOwnerIdentityConfiguration(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.RegisterOwnerIdentityReturns(nil)

	ctx := context.Background()
	conf := IdentityConfiguration{ID: "alice", URL: "/path"}
	err := wm.RegisterOwnerIdentityConfiguration(ctx, conf)

	require.NoError(t, err)
	assert.Equal(t, 1, mockWS.RegisterOwnerIdentityCallCount())
}

// TestWalletManager_RegisterIssuerIdentity verifies issuer identity registration
func TestWalletManager_RegisterIssuerIdentity(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.RegisterIssuerIdentityReturns(nil)

	ctx := context.Background()
	err := wm.RegisterIssuerIdentity(ctx, "issuer1", "/path/to/issuer")

	require.NoError(t, err)
	assert.Equal(t, 1, mockWS.RegisterIssuerIdentityCallCount())
}

// TestWalletManager_RegisterRecipientIdentity verifies recipient identity registration
func TestWalletManager_RegisterRecipientIdentity(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.RegisterRecipientIdentityReturns(nil)

	ctx := context.Background()
	data := &RecipientData{}
	err := wm.RegisterRecipientIdentity(ctx, data)

	require.NoError(t, err)
	assert.Equal(t, 1, mockWS.RegisterRecipientIdentityCallCount())
}

// TestWalletManager_Wallet verifies Wallet retrieval
func TestWalletManager_Wallet(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockW := &mock.Wallet{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.WalletReturns(mockW)

	ctx := context.Background()
	identity := Identity([]byte("alice"))
	wallet := wm.Wallet(ctx, identity)

	assert.NotNil(t, wallet)
	assert.Equal(t, mockW, wallet.w)
}

// TestWalletManager_Wallet_Nil verifies nil wallet handling
func TestWalletManager_Wallet_Nil(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.WalletReturns(nil)

	ctx := context.Background()
	wallet := wm.Wallet(ctx, Identity([]byte("unknown")))

	assert.Nil(t, wallet)
}

// TestWalletManager_OwnerWalletIDs verifies owner wallet IDs retrieval
func TestWalletManager_OwnerWalletIDs(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	expectedIDs := []string{"wallet1", "wallet2"}
	mockWS.OwnerWalletIDsReturns(expectedIDs, nil)

	ctx := context.Background()
	ids, err := wm.OwnerWalletIDs(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedIDs, ids)
}

// TestWalletManager_OwnerWallet verifies owner wallet retrieval
func TestWalletManager_OwnerWallet(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockOW := &mock.OwnerWallet{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.OwnerWalletReturns(mockOW, nil)

	ctx := context.Background()
	wallet, err := wm.OwnerWallet(ctx, "alice")

	require.NoError(t, err)
	assert.NotNil(t, wallet)
	assert.Equal(t, mockOW, wallet.w)
}

// TestWalletManager_OwnerWallet_Error verifies error handling
func TestWalletManager_OwnerWallet_Error(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockMS := &ManagementService{
		logger: logging.MustGetLogger(),
	}
	wm := &WalletManager{walletService: mockWS, managementService: mockMS}

	mockWS.OwnerWalletReturns(nil, errors.New("wallet not found"))

	ctx := context.Background()
	wallet, err := wm.OwnerWallet(ctx, "unknown")

	require.Error(t, err)
	assert.Nil(t, wallet)
}

// TestWalletManager_IssuerWallet verifies issuer wallet retrieval
func TestWalletManager_IssuerWallet(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockIW := &mock.IssuerWallet{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.IssuerWalletReturns(mockIW, nil)

	ctx := context.Background()
	wallet, err := wm.IssuerWallet(ctx, "issuer1")

	require.NoError(t, err)
	assert.NotNil(t, wallet)
	assert.Equal(t, mockIW, wallet.w)
}

// TestWalletManager_AuditorWallet verifies auditor wallet retrieval
func TestWalletManager_AuditorWallet(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockAW := &mock.AuditorWallet{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.AuditorWalletReturns(mockAW, nil)

	ctx := context.Background()
	wallet, err := wm.AuditorWallet(ctx, "auditor1")

	require.NoError(t, err)
	assert.NotNil(t, wallet)
	assert.Equal(t, mockAW, wallet.w)
}

// TestWalletManager_CertifierWallet verifies certifier wallet retrieval
func TestWalletManager_CertifierWallet(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockCW := &mock.CertifierWallet{}
	wm := &WalletManager{walletService: mockWS}

	mockWS.CertifierWalletReturns(mockCW, nil)

	ctx := context.Background()
	wallet, err := wm.CertifierWallet(ctx, "certifier1")

	require.NoError(t, err)
	assert.NotNil(t, wallet)
	assert.Equal(t, mockCW, wallet.w)
}

// TestWalletManager_GetEnrollmentID verifies enrollment ID retrieval
func TestWalletManager_GetEnrollmentID(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	identity := Identity([]byte("alice"))
	auditInfo := []byte("audit")
	mockWS.GetAuditInfoReturns(auditInfo, nil)
	mockWS.GetEnrollmentIDReturns("alice-eid", nil)

	ctx := context.Background()
	eid, err := wm.GetEnrollmentID(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, "alice-eid", eid)
}

// TestWalletManager_GetRevocationHandle verifies revocation handle retrieval
func TestWalletManager_GetRevocationHandle(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	identity := Identity([]byte("alice"))
	auditInfo := []byte("audit")
	mockWS.GetAuditInfoReturns(auditInfo, nil)
	mockWS.GetRevocationHandleReturns("revocation-handle", nil)

	ctx := context.Background()
	rh, err := wm.GetRevocationHandle(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, "revocation-handle", rh)
}

// TestWalletManager_SpentIDs verifies spent IDs retrieval
func TestWalletManager_SpentIDs(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	ids := []*token.ID{{TxId: "tx1", Index: 0}}
	expectedSpentIDs := []string{"spent1"}
	mockWS.SpendIDsReturns(expectedSpentIDs, nil)

	spentIDs, err := wm.SpentIDs(ids)

	require.NoError(t, err)
	assert.Equal(t, expectedSpentIDs, spentIDs)
}

// TestWallet_ID verifies Wallet ID getter
func TestWallet_ID(t *testing.T) {
	mockW := &mock.Wallet{}
	mockW.IDReturns("wallet-id")
	wallet := &Wallet{w: mockW}

	id := wallet.ID()

	assert.Equal(t, "wallet-id", id)
}

// TestWallet_TMS verifies TMS getter
func TestWallet_TMS(t *testing.T) {
	mockTMS := &ManagementService{}
	wallet := &Wallet{managementService: mockTMS}

	tms := wallet.TMS()

	assert.Equal(t, mockTMS, tms)
}

// TestWallet_Contains verifies Contains method
func TestWallet_Contains(t *testing.T) {
	mockW := &mock.Wallet{}
	mockW.ContainsReturns(true)
	wallet := &Wallet{w: mockW}

	ctx := context.Background()
	identity := driver.Identity([]byte("alice"))
	contains := wallet.Contains(ctx, identity)

	assert.True(t, contains)
}

// TestWallet_ContainsToken verifies ContainsToken method
func TestWallet_ContainsToken(t *testing.T) {
	mockW := &mock.Wallet{}
	mockW.ContainsTokenReturns(true)
	wallet := &Wallet{w: mockW}

	ctx := context.Background()
	tok := &token.UnspentToken{}
	contains := wallet.ContainsToken(ctx, tok)

	assert.True(t, contains)
}

// TestCompileListTokensOption verifies option compilation
func TestCompileListTokensOption(t *testing.T) {
	opts, err := CompileListTokensOption(
		WithType("USD"),
	)

	require.NoError(t, err)
	assert.Equal(t, token.Type("USD"), opts.TokenType)
}

// TestCompileListTokensOption_DefaultContext verifies options compile with no args
func TestCompileListTokensOption_DefaultContext(t *testing.T) {
	opts, err := CompileListTokensOption()

	require.NoError(t, err)
	assert.NotNil(t, opts)
}

// TestAuditorWallet_GetAuditorIdentity verifies GetAuditorIdentity
func TestAuditorWallet_GetAuditorIdentity(t *testing.T) {
	mockAW := &mock.AuditorWallet{}
	expectedIdentity := Identity([]byte("auditor"))
	mockAW.GetAuditorIdentityReturns(expectedIdentity, nil)

	wallet := &AuditorWallet{w: mockAW}
	identity, err := wallet.GetAuditorIdentity()

	require.NoError(t, err)
	assert.Equal(t, expectedIdentity, identity)
}

// TestCertifierWallet_GetCertifierIdentity verifies GetCertifierIdentity
func TestCertifierWallet_GetCertifierIdentity(t *testing.T) {
	mockCW := &mock.CertifierWallet{}
	expectedIdentity := Identity([]byte("certifier"))
	mockCW.GetCertifierIdentityReturns(expectedIdentity, nil)

	wallet := &CertifierWallet{w: mockCW}
	identity, err := wallet.GetCertifierIdentity()

	require.NoError(t, err)
	assert.Equal(t, expectedIdentity, identity)
}

// TestOwnerWallet_EnrollmentID verifies EnrollmentID getter
func TestOwnerWallet_EnrollmentID(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	mockOW.EnrollmentIDReturns("alice-eid")

	wallet := &OwnerWallet{w: mockOW}
	eid := wallet.EnrollmentID()

	assert.Equal(t, "alice-eid", eid)
}

// TestOwnerWallet_Remote verifies Remote getter
func TestOwnerWallet_Remote(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	mockOW.RemoteReturns(true)

	wallet := &OwnerWallet{w: mockOW}
	remote := wallet.Remote()

	assert.True(t, remote)
}

// TestIssuerWallet_GetIssuerIdentity verifies GetIssuerIdentity
func TestIssuerWallet_GetIssuerIdentity(t *testing.T) {
	mockIW := &mock.IssuerWallet{}
	expectedIdentity := Identity([]byte("issuer"))
	mockIW.GetIssuerIdentityReturns(expectedIdentity, nil)

	wallet := &IssuerWallet{w: mockIW}
	identity, err := wallet.GetIssuerIdentity("USD")

	require.NoError(t, err)
	assert.Equal(t, expectedIdentity, identity)
}

// TestAuditorWallet_GetSigner verifies GetSigner for auditor
func TestAuditorWallet_GetSigner(t *testing.T) {
	mockAW := &mock.AuditorWallet{}
	mockSigner := &mock.Signer{}
	mockAW.GetSignerReturns(mockSigner, nil)

	wallet := &AuditorWallet{w: mockAW}
	ctx := context.Background()
	identity := driver.Identity([]byte("auditor"))

	signer, err := wallet.GetSigner(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, mockSigner, signer)
}

// TestCertifierWallet_GetSigner verifies GetSigner for certifier
func TestCertifierWallet_GetSigner(t *testing.T) {
	mockCW := &mock.CertifierWallet{}
	mockSigner := &mock.Signer{}
	mockCW.GetSignerReturns(mockSigner, nil)

	wallet := &CertifierWallet{w: mockCW}
	ctx := context.Background()
	identity := driver.Identity([]byte("certifier"))

	signer, err := wallet.GetSigner(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, mockSigner, signer)
}

// TestOwnerWallet_GetRecipientIdentity verifies GetRecipientIdentity
func TestOwnerWallet_GetRecipientIdentity(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedIdentity := Identity([]byte("owner"))
	mockOW.GetRecipientIdentityReturns(expectedIdentity, nil)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()

	identity, err := wallet.GetRecipientIdentity(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedIdentity, identity)
}

// TestOwnerWallet_GetRecipientData verifies GetRecipientData
func TestOwnerWallet_GetRecipientData(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedData := &RecipientData{}
	mockOW.GetRecipientDataReturns(expectedData, nil)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()

	data, err := wallet.GetRecipientData(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

// TestOwnerWallet_GetAuditInfo verifies GetAuditInfo
func TestOwnerWallet_GetAuditInfo(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedAuditInfo := []byte("audit-info")
	mockOW.GetAuditInfoReturns(expectedAuditInfo, nil)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()
	identity := Identity([]byte("owner"))

	auditInfo, err := wallet.GetAuditInfo(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, expectedAuditInfo, auditInfo)
}

// TestOwnerWallet_GetTokenMetadata verifies GetTokenMetadata
func TestOwnerWallet_GetTokenMetadata(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedMetadata := []byte("token-metadata")
	mockOW.GetTokenMetadataReturns(expectedMetadata, nil)

	wallet := &OwnerWallet{w: mockOW}
	tokenBytes := []byte("token")

	metadata, err := wallet.GetTokenMetadata(tokenBytes)

	require.NoError(t, err)
	assert.Equal(t, expectedMetadata, metadata)
}

// TestOwnerWallet_GetTokenMetadataAuditInfo verifies GetTokenMetadataAuditInfo
func TestOwnerWallet_GetTokenMetadataAuditInfo(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedAuditInfo := []byte("metadata-audit-info")
	mockOW.GetTokenMetadataAuditInfoReturns(expectedAuditInfo, nil)

	wallet := &OwnerWallet{w: mockOW}
	tokenBytes := []byte("token")

	auditInfo, err := wallet.GetTokenMetadataAuditInfo(tokenBytes)

	require.NoError(t, err)
	assert.Equal(t, expectedAuditInfo, auditInfo)
}

// TestOwnerWallet_GetSigner verifies GetSigner for owner
func TestOwnerWallet_GetSigner(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	mockSigner := &mock.Signer{}
	mockOW.GetSignerReturns(mockSigner, nil)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()
	identity := driver.Identity([]byte("owner"))

	signer, err := wallet.GetSigner(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, mockSigner, signer)
}

// TestOwnerWallet_ListUnspentTokens verifies ListUnspentTokens
func TestOwnerWallet_ListUnspentTokens(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedTokens := &token.UnspentTokens{}
	mockOW.ListTokensReturns(expectedTokens, nil)

	wallet := &OwnerWallet{w: mockOW}

	tokens, err := wallet.ListUnspentTokens(context.Background(), WithType("USD"))

	require.NoError(t, err)
	assert.Equal(t, expectedTokens, tokens)
}

// TestOwnerWallet_ListUnspentTokensIterator verifies ListUnspentTokensIterator
func TestOwnerWallet_ListUnspentTokensIterator(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	mockIterator := &mock.UnspentTokensIterator{}
	mockOW.ListTokensIteratorReturns(mockIterator, nil)

	wallet := &OwnerWallet{w: mockOW}

	iterator, err := wallet.ListUnspentTokensIterator(context.Background(), WithType("USD"))

	require.NoError(t, err)
	assert.NotNil(t, iterator)
	assert.Equal(t, mockIterator, iterator.UnspentTokensIterator)
}

// TestOwnerWallet_Balance verifies Balance
func TestOwnerWallet_Balance(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	mockOW.BalanceReturns(big.NewInt(1000), nil)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()

	balance, err := wallet.Balance(ctx, WithType("USD"))

	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1000), balance)
}

// TestOwnerWallet_RegisterRecipient verifies RegisterRecipient
func TestOwnerWallet_RegisterRecipient(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	mockOW.RegisterRecipientReturns(nil)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()
	data := &RecipientData{}

	err := wallet.RegisterRecipient(ctx, data)

	require.NoError(t, err)
}

// TestIssuerWallet_GetSigner verifies GetSigner for issuer
func TestIssuerWallet_GetSigner(t *testing.T) {
	mockIW := &mock.IssuerWallet{}
	mockSigner := &mock.Signer{}
	mockIW.GetSignerReturns(mockSigner, nil)

	wallet := &IssuerWallet{w: mockIW}
	ctx := context.Background()
	identity := driver.Identity([]byte("issuer"))

	signer, err := wallet.GetSigner(ctx, identity)

	require.NoError(t, err)
	assert.Equal(t, mockSigner, signer)
}

// TestIssuerWallet_ListIssuedTokens verifies ListIssuedTokens
func TestIssuerWallet_ListIssuedTokens(t *testing.T) {
	mockIW := &mock.IssuerWallet{}
	expectedTokens := &token.IssuedTokens{}
	mockIW.HistoryTokensReturns(expectedTokens, nil)

	wallet := &IssuerWallet{w: mockIW}
	ctx := context.Background()

	tokens, err := wallet.ListIssuedTokens(ctx, WithType("USD"))

	require.NoError(t, err)
	assert.Equal(t, expectedTokens, tokens)
}

// TestWalletManager_OwnerWalletIDs_Error verifies error handling
func TestWalletManager_OwnerWalletIDs_Error(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	expectedErr := errors.New("failed to get wallet IDs")
	mockWS.OwnerWalletIDsReturns(nil, expectedErr)

	ctx := context.Background()
	ids, err := wm.OwnerWalletIDs(ctx)

	require.Error(t, err)
	assert.Nil(t, ids)
	assert.Contains(t, err.Error(), "failed to get the list of owner wallet identifiers")
}

// TestWalletManager_IssuerWallet_Nil verifies nil issuer wallet handling
func TestWalletManager_IssuerWallet_Nil(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockMS := &ManagementService{
		logger: logging.MustGetLogger(),
	}
	wm := &WalletManager{walletService: mockWS, managementService: mockMS}

	mockWS.IssuerWalletReturns(nil, errors.New("wallet not found"))

	ctx := context.Background()
	wallet, err := wm.IssuerWallet(ctx, "unknown")

	require.Error(t, err)
	assert.Nil(t, wallet)
}

// TestWalletManager_AuditorWallet_Nil verifies nil auditor wallet handling
func TestWalletManager_AuditorWallet_Nil(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockMS := &ManagementService{
		logger: logging.MustGetLogger(),
	}
	wm := &WalletManager{walletService: mockWS, managementService: mockMS}

	mockWS.AuditorWalletReturns(nil, errors.New("wallet not found"))

	ctx := context.Background()
	wallet, err := wm.AuditorWallet(ctx, "unknown")

	require.Error(t, err)
	assert.Nil(t, wallet)
}

// TestWalletManager_CertifierWallet_Nil verifies nil certifier wallet handling
func TestWalletManager_CertifierWallet_Nil(t *testing.T) {
	mockWS := &mock.WalletService{}
	mockMS := &ManagementService{
		logger: logging.MustGetLogger(),
	}
	wm := &WalletManager{walletService: mockWS, managementService: mockMS}

	mockWS.CertifierWalletReturns(nil, errors.New("wallet not found"))

	ctx := context.Background()
	wallet, err := wm.CertifierWallet(ctx, "unknown")

	require.Error(t, err)
	assert.Nil(t, wallet)
}

// TestWalletManager_GetEnrollmentID_Error verifies error handling
func TestWalletManager_GetEnrollmentID_Error(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	identity := Identity([]byte("alice"))
	expectedErr := errors.New("failed to get audit info")
	mockWS.GetAuditInfoReturns(nil, expectedErr)

	ctx := context.Background()
	eid, err := wm.GetEnrollmentID(ctx, identity)

	require.Error(t, err)
	assert.Empty(t, eid)
	assert.Contains(t, err.Error(), "failed to get audit info")
}

// TestWalletManager_GetRevocationHandle_Error verifies error handling
func TestWalletManager_GetRevocationHandle_Error(t *testing.T) {
	mockWS := &mock.WalletService{}
	wm := &WalletManager{walletService: mockWS}

	identity := Identity([]byte("alice"))
	expectedErr := errors.New("failed to get audit info")
	mockWS.GetAuditInfoReturns(nil, expectedErr)

	ctx := context.Background()
	rh, err := wm.GetRevocationHandle(ctx, identity)

	require.Error(t, err)
	assert.Empty(t, rh)
	assert.Contains(t, err.Error(), "failed to get audit info")
}

// TestOwnerWallet_ListUnspentTokens_Error verifies error handling
func TestOwnerWallet_ListUnspentTokens_Error(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedErr := errors.New("failed to list tokens")
	mockOW.ListTokensReturns(nil, expectedErr)

	wallet := &OwnerWallet{w: mockOW}

	tokens, err := wallet.ListUnspentTokens(context.Background(), WithType("USD"))

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, tokens)
}

// TestOwnerWallet_ListUnspentTokensIterator_Error verifies error handling
func TestOwnerWallet_ListUnspentTokensIterator_Error(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedErr := errors.New("failed to create iterator")
	mockOW.ListTokensIteratorReturns(nil, expectedErr)

	wallet := &OwnerWallet{w: mockOW}

	iterator, err := wallet.ListUnspentTokensIterator(context.Background(), WithType("USD"))

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, iterator)
}

// TestOwnerWallet_Balance_Error verifies error handling
func TestOwnerWallet_Balance_Error(t *testing.T) {
	mockOW := &mock.OwnerWallet{}
	expectedErr := errors.New("failed to get balance")
	mockOW.BalanceReturns(big.NewInt(0), expectedErr)

	wallet := &OwnerWallet{w: mockOW}
	ctx := context.Background()

	balance, err := wallet.Balance(ctx, WithType("USD"))

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, balance)
}

// TestIssuerWallet_ListIssuedTokens_Error verifies error handling
func TestIssuerWallet_ListIssuedTokens_Error(t *testing.T) {
	mockIW := &mock.IssuerWallet{}
	expectedErr := errors.New("failed to list issued tokens")
	mockIW.HistoryTokensReturns(nil, expectedErr)

	wallet := &IssuerWallet{w: mockIW}
	ctx := context.Background()

	tokens, err := wallet.ListIssuedTokens(ctx, WithType("USD"))

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, tokens)
}

// TestCompileListTokensOption_Error verifies error handling
func TestCompileListTokensOption_Error(t *testing.T) {
	errorOption := func(o *ListTokensOptions) error {
		return errors.New("option error")
	}

	opts, err := CompileListTokensOption(errorOption)

	require.Error(t, err)
	assert.Nil(t, opts)
}

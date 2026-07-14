/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Manual mocks are defined in provider_test.go to avoid duplication

// TestNewManagementService verifies ManagementService constructor
func TestNewManagementService(t *testing.T) {
	tmsID := TMSID{
		Network:   "testnet",
		Channel:   "testchannel",
		Namespace: "testns",
	}

	mockTMS := &mock.TokenManagerService{}
	mockVault := &mock.Vault{}
	mockValidator := &mock.Validator{}
	mockAuth := &mock.Authorization{}
	mockConfig := &mock.Configuration{}
	mockTokensService := &mock.TokensService{}
	mockTokensUpgradeService := &mock.TokensUpgradeService{}
	mockWalletService := &mock.WalletService{}
	mockPPM := &mock.PublicParamsManager{}
	mockPP := &mock.PublicParameters{}
	mockDeserializer := &mock.Deserializer{}
	mockIdentityProvider := &mock.IdentityProvider{}

	vaultProvider := &mockVaultProvider{vault: mockVault}
	certProvider := &mockCertificationClientProvider{}
	selectorProvider := &mockSelectorManagerProvider{}

	mockTMS.ValidatorReturns(mockValidator, nil)
	mockTMS.AuthorizationReturns(mockAuth)
	mockTMS.ConfigurationReturns(mockConfig)
	mockTMS.TokensServiceReturns(mockTokensService)
	mockTMS.TokensUpgradeServiceReturns(mockTokensUpgradeService)
	mockTMS.WalletServiceReturns(mockWalletService)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockPPM.PublicParametersReturns(mockPP)
	mockTMS.DeserializerReturns(mockDeserializer)
	mockTMS.IdentityProviderReturns(mockIdentityProvider)
	mockVault.CertificationStorageReturns(nil)
	mockTMS.CertificationServiceReturns(nil)

	ms, err := NewManagementService(
		tmsID,
		mockTMS,
		logging.MustGetLogger(),
		vaultProvider,
		certProvider,
		selectorProvider,
	)

	require.NoError(t, err)
	assert.NotNil(t, ms)
	assert.Equal(t, tmsID, ms.id)
	assert.Equal(t, mockTMS, ms.tms)
}

// TestManagementService_String verifies String representation
func TestManagementService_String(t *testing.T) {
	ms := &ManagementService{
		id: TMSID{
			Network:   "net1",
			Channel:   "ch1",
			Namespace: "ns1",
		},
	}

	str := ms.String()
	assert.Contains(t, str, "net1")
	assert.Contains(t, str, "ch1")
}

// TestManagementService_Network verifies Network getter
func TestManagementService_Network(t *testing.T) {
	ms := &ManagementService{
		id: TMSID{Network: "testnetwork"},
	}

	assert.Equal(t, "testnetwork", ms.Network())
}

// TestManagementService_Channel verifies Channel getter
func TestManagementService_Channel(t *testing.T) {
	ms := &ManagementService{
		id: TMSID{Channel: "testchannel"},
	}

	assert.Equal(t, "testchannel", ms.Channel())
}

// TestManagementService_Namespace verifies Namespace getter
func TestManagementService_Namespace(t *testing.T) {
	ms := &ManagementService{
		id: TMSID{Namespace: "testnamespace"},
	}

	assert.Equal(t, "testnamespace", ms.Namespace())
}

// TestManagementService_ID verifies ID getter
func TestManagementService_ID(t *testing.T) {
	tmsID := TMSID{
		Network:   "net",
		Channel:   "ch",
		Namespace: "ns",
	}
	ms := &ManagementService{id: tmsID}

	result := ms.ID()
	assert.Equal(t, tmsID, result)
}

// TestManagementService_Vault verifies Vault getter
func TestManagementService_Vault(t *testing.T) {
	mockVault := &Vault{}
	ms := &ManagementService{vault: mockVault}

	result := ms.Vault()
	assert.Equal(t, mockVault, result)
}

// TestManagementService_WalletManager verifies WalletManager getter
func TestManagementService_WalletManager(t *testing.T) {
	mockWM := &WalletManager{}
	ms := &ManagementService{walletManager: mockWM}

	result := ms.WalletManager()
	assert.Equal(t, mockWM, result)
}

// TestManagementService_Validator verifies Validator getter
func TestManagementService_Validator(t *testing.T) {
	mockValidator := &Validator{}
	ms := &ManagementService{validator: mockValidator}

	result, err := ms.Validator()
	require.NoError(t, err)
	assert.Equal(t, mockValidator, result)
}

// TestManagementService_PublicParametersManager verifies PublicParametersManager getter
func TestManagementService_PublicParametersManager(t *testing.T) {
	mockPPM := &PublicParametersManager{}
	ms := &ManagementService{publicParametersManager: mockPPM}

	result := ms.PublicParametersManager()
	assert.Equal(t, mockPPM, result)
}

// TestManagementService_Configuration verifies Configuration getter
func TestManagementService_Configuration(t *testing.T) {
	mockConf := &Configuration{}
	ms := &ManagementService{conf: mockConf}

	result := ms.Configuration()
	assert.Equal(t, mockConf, result)
}

// TestManagementService_Authorization verifies Authorization getter
func TestManagementService_Authorization(t *testing.T) {
	mockAuth := &Authorization{}
	ms := &ManagementService{auth: mockAuth}

	result := ms.Authorization()
	assert.Equal(t, mockAuth, result)
}

// TestManagementService_TokensService verifies TokensService getter
func TestManagementService_TokensService(t *testing.T) {
	mockTS := &TokensService{}
	ms := &ManagementService{tokensService: mockTS}

	result := ms.TokensService()
	assert.Equal(t, mockTS, result)
}

// TestManagementService_CertificationManager verifies CertificationManager getter
func TestManagementService_CertificationManager(t *testing.T) {
	mockCM := &CertificationManager{}
	ms := &ManagementService{certificationManager: mockCM}

	result := ms.CertificationManager()
	assert.Equal(t, mockCM, result)
}

// TestManagementService_CertificationManager_Nil verifies nil certification manager
func TestManagementService_CertificationManager_Nil(t *testing.T) {
	ms := &ManagementService{}

	result := ms.CertificationManager()
	assert.Nil(t, result)
}

// TestManagementService_SigService verifies SigService getter
func TestManagementService_SigService(t *testing.T) {
	mockSS := &SignatureService{}
	ms := &ManagementService{signatureService: mockSS}

	result := ms.SigService()
	assert.Equal(t, mockSS, result)
}

// TestManagementService_CertificationClient verifies CertificationClient creation
func TestManagementService_CertificationClient(t *testing.T) {
	mockCC := &mock.CertificationClient{}
	certProvider := &mockCertificationClientProvider{cc: mockCC}

	ms := &ManagementService{
		certificationClientProvider: certProvider,
	}

	result, err := ms.CertificationClient(t.Context())

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, mockCC, result.cc)
}

// TestManagementService_CertificationClient_Error verifies error handling
func TestManagementService_CertificationClient_Error(t *testing.T) {
	expectedErr := errors.New("failed instantiating certification manager with driver [test-driver]")
	certProvider := &mockCertificationClientProvider{err: expectedErr}

	ms := &ManagementService{
		certificationClientProvider: certProvider,
	}

	result, err := ms.CertificationClient(t.Context())

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create certification client")
	assert.ErrorIs(t, err, expectedErr)
}

// TestManagementService_SelectorManager verifies SelectorManager creation
func TestManagementService_SelectorManager(t *testing.T) {
	mockSM := &mockSelectorManager{}
	selectorProvider := &mockSelectorManagerProvider{sm: mockSM}

	ms := &ManagementService{
		selectorManagerProvider: selectorProvider,
	}

	result, err := ms.SelectorManager()

	require.NoError(t, err)
	assert.Equal(t, mockSM, result)
}

// TestManagementService_NewRequest verifies NewRequest creation
func TestManagementService_NewRequest(t *testing.T) {
	ms := &ManagementService{}
	var anchor RequestAnchor = "test-anchor"

	req, err := ms.NewRequest(anchor)

	require.NoError(t, err)
	assert.NotNil(t, req)
}

// TestGetManagementService verifies GetManagementService function
func TestGetManagementService(t *testing.T) {
	mockTMS := &mock.TokenManagerService{}
	mockVault := &mock.Vault{}
	mockValidator := &mock.Validator{}
	mockAuth := &mock.Authorization{}
	mockConfig := &mock.Configuration{}
	mockTokensService := &mock.TokensService{}
	mockTokensUpgradeService := &mock.TokensUpgradeService{}
	mockWalletService := &mock.WalletService{}
	mockPPM := &mock.PublicParamsManager{}
	mockPP := &mock.PublicParameters{}
	mockDeserializer := &mock.Deserializer{}
	mockIdentityProvider := &mock.IdentityProvider{}

	mockTMS.ValidatorReturns(mockValidator, nil)
	mockTMS.AuthorizationReturns(mockAuth)
	mockTMS.ConfigurationReturns(mockConfig)
	mockTMS.TokensServiceReturns(mockTokensService)
	mockTMS.TokensUpgradeServiceReturns(mockTokensUpgradeService)
	mockTMS.WalletServiceReturns(mockWalletService)
	mockTMS.PublicParamsManagerReturns(mockPPM)
	mockPPM.PublicParametersReturns(mockPP)
	mockTMS.DeserializerReturns(mockDeserializer)
	mockTMS.IdentityProviderReturns(mockIdentityProvider)
	mockVault.CertificationStorageReturns(nil)
	mockTMS.CertificationServiceReturns(nil)

	tmsProvider := &mockTokenManagerServiceProvider{tms: mockTMS}
	vaultProvider := &mockVaultProvider{vault: mockVault}
	normalizer := &mockNormalizer{}
	certProvider := &mockCertificationClientProvider{}
	selectorProvider := &mockSelectorManagerProvider{}

	mockMSP := NewManagementServiceProvider(
		tmsProvider,
		normalizer,
		vaultProvider,
		certProvider,
		selectorProvider,
	)

	sp := &mockServiceProvider{service: mockMSP}

	result, err := GetManagementService(sp)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestNewWalletManager verifies NewWalletManager constructor
func TestNewWalletManager(t *testing.T) {
	mockWS := &mock.WalletService{}

	wm := NewWalletManager(mockWS)

	assert.NotNil(t, wm)
	assert.Equal(t, mockWS, wm.walletService)
}

// TestManagementService_NewRequestFromBytes verifies request deserialization from bytes
func TestManagementService_NewRequestFromBytes(t *testing.T) {
	mockTMS := &mock.TokenManagerService{}
	mockTokensService := &mock.TokensService{}
	mockWalletService := &mock.WalletService{}

	mockTMS.TokensServiceReturns(mockTokensService)
	mockTMS.WalletServiceReturns(mockWalletService)

	ms := &ManagementService{
		tms:    mockTMS,
		logger: logging.MustGetLogger(),
	}

	// Create a valid request to serialize
	original := NewRequest(ms, "test-anchor")
	original.Actions = &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
	}

	actionsBytes, err := original.Actions.Bytes()
	require.NoError(t, err)

	metadataBytes, err := original.Metadata.Bytes()
	require.NoError(t, err)

	// Test deserialization
	restored, err := ms.NewRequestFromBytes("test-anchor", actionsBytes, metadataBytes)
	require.NoError(t, err)
	assert.NotNil(t, restored)
	assert.Equal(t, RequestAnchor("test-anchor"), restored.Anchor)
}

// TestManagementService_NewFullRequestFromBytes verifies full request deserialization
func TestManagementService_NewFullRequestFromBytes(t *testing.T) {
	mockTMS := &mock.TokenManagerService{}
	mockTokensService := &mock.TokensService{}
	mockWalletService := &mock.WalletService{}

	mockTMS.TokensServiceReturns(mockTokensService)
	mockTMS.WalletServiceReturns(mockWalletService)

	ms := &ManagementService{
		tms:    mockTMS,
		logger: logging.MustGetLogger(),
	}

	// Create a valid request to serialize
	original := NewRequest(ms, "test-anchor")
	original.Actions = &driver.TokenRequest{
		Actions: []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("issue1")},
		},
	}

	fullBytes, err := original.Bytes()
	require.NoError(t, err)

	// Test deserialization
	restored, err := ms.NewFullRequestFromBytes(fullBytes)
	require.NoError(t, err)
	assert.NotNil(t, restored)
	assert.Equal(t, RequestAnchor("test-anchor"), restored.Anchor)
}

// TestManagementService_NewMetadataFromBytes verifies metadata deserialization
func TestManagementService_NewMetadataFromBytes(t *testing.T) {
	mockTMS := &mock.TokenManagerService{}
	mockTokensService := &mock.TokensService{}
	mockWalletService := &mock.WalletService{}

	mockTMS.TokensServiceReturns(mockTokensService)
	mockTMS.WalletServiceReturns(mockWalletService)

	ms := &ManagementService{
		tms:    mockTMS,
		logger: logging.MustGetLogger(),
	}

	// Create metadata to serialize
	original := &driver.TokenRequestMetadata{
		Actions: []*driver.ActionMetadataEntry{
			{
				ActionID: 0,
				IssueMetadata: &driver.IssueMetadata{
					Issuer: driver.AuditableIdentity{
						Identity:  driver.Identity([]byte("issuer1")),
						AuditInfo: []byte("audit1"),
					},
				},
			},
		},
	}

	metadataBytes, err := original.Bytes()
	require.NoError(t, err)

	// Test deserialization
	restored, err := ms.NewMetadataFromBytes(metadataBytes)
	require.NoError(t, err)
	assert.NotNil(t, restored)
	assert.NotNil(t, restored.TokenRequestMetadata)
	assert.Len(t, restored.TokenRequestMetadata.Actions, 1)
}

// TestManagementService_NewMetadataFromBytes_Error verifies error handling
func TestManagementService_NewMetadataFromBytes_Error(t *testing.T) {
	mockTMS := &mock.TokenManagerService{}

	ms := &ManagementService{
		tms:    mockTMS,
		logger: logging.MustGetLogger(),
	}

	// Test with invalid bytes
	_, err := ms.NewMetadataFromBytes([]byte("invalid"))
	require.Error(t, err)
}

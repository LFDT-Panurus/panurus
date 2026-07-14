/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	tokenmock "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	tokensmock "github.com/LFDT-Panurus/panurus/token/services/tokens/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceManager(t *testing.T) {
	tmsID := token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"}
	mockTMS := &mock.TokenManagerService{}
	mockConfig := &mock.Configuration{}
	mockTMS.ConfigurationReturns(mockConfig)

	mockPPM := &mock.PublicParamsManager{}
	mockPP := &mock.PublicParameters{}
	mockPPM.PublicParametersReturns(mockPP)
	mockTMS.PublicParamsManagerReturns(mockPPM)

	mockTMS.DeserializerReturns(&mock.Deserializer{})
	mockTMS.IdentityProviderReturns(&mock.IdentityProvider{})

	mockVP := &tokenmock.VaultProvider{}
	mockVault := &mock.Vault{}
	mockVP.VaultReturns(mockVault, nil)

	tms, err := token.NewManagementService(tmsID, mockTMS, nil, mockVP, nil, nil)
	require.NoError(t, err)

	mockTMSProv := &tokensmock.FakeTMSProvider{}
	mockTMSProv.GetManagementServiceReturns(tms, nil)
	mockStoreServProv := &tokensmock.FakeStoreServiceManager{}

	mockNetProv := &tokensmock.FakeNetworkProvider{}
	mockPub := &tokensmock.FakePublisher{}

	manager := tokens.NewServiceManager(mockTMSProv, mockStoreServProv, mockNetProv, mockPub)
	require.NotNil(t, manager)

	// Test ServiceByTMSId
	service, err := manager.ServiceByTMSId(tmsID)
	require.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, mockTMSProv, service.TMSProvider)
	assert.Equal(t, mockNetProv, service.NetworkProvider)

	// Test GetService helper
	sp := &tokensmock.FakeServiceProvider{}
	sp.GetServiceReturns(manager, nil)

	service2, err := tokens.GetService(sp, tmsID)
	require.NoError(t, err)
	assert.Equal(t, service, service2)

	// Test GetService error
	spErr := &tokensmock.FakeServiceProvider{}
	spErr.GetServiceReturns(nil, assert.AnError)

	_, err = tokens.GetService(spErr, tmsID)
	assert.Error(t, err)
}

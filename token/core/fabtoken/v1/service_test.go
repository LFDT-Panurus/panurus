/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1_test

import (
	"testing"

	v1 "github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewService verifies the creation of a new fabtoken service and its components.
func TestNewService(t *testing.T) {
	logger := logging.MustGetLogger("test")
	ws := &mock.WalletService{}
	ppm := &mockPublicParamsManager{PublicParamsManager: &mock.PublicParamsManager{}}
	ip := &mock.IdentityProvider{}
	deserializer := &mock.Deserializer{}
	config := &mock.Configuration{}
	issueService := &mock.IssueService{}
	transferService := &mock.TransferService{}
	auditorService := &mock.AuditorService{}
	tokensService := &mock.TokensService{}
	tokensUpgradeService := &mock.TokensUpgradeService{}
	authorization := &mock.Authorization{}
	validator := &mock.Validator{}

	service, err := v1.NewService(
		logger,
		ws,
		ppm,
		ip,
		deserializer,
		config,
		issueService,
		transferService,
		auditorService,
		tokensService,
		tokensUpgradeService,
		authorization,
		validator,
	)
	require.NoError(t, err)
	assert.NotNil(t, service)

	// Verify that the components are correctly set and accessible via driver.TokenManagerService interface
	assert.Equal(t, ip, service.IdentityProvider())
	assert.Equal(t, deserializer, service.Deserializer())
	assert.Equal(t, ppm, service.PublicParamsManager())
	assert.Equal(t, config, service.Configuration())
	assert.Equal(t, ws, service.WalletService())
	assert.Equal(t, issueService, service.IssueService())
	assert.Equal(t, transferService, service.TransferService())
	assert.Equal(t, auditorService, service.AuditorService())
	assert.Equal(t, tokensService, service.TokensService())
	assert.Equal(t, tokensUpgradeService, service.TokensUpgradeService())
	assert.Equal(t, authorization, service.Authorization())

	v, err := service.Validator()
	require.NoError(t, err)
	assert.Equal(t, validator, v)

	assert.Nil(t, service.CertificationService())

	err = service.Done()
	require.NoError(t, err)
}

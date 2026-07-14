/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublicParameters_Precision verifies retrieval of precision from public parameters
func TestPublicParameters_Precision(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.PrecisionReturns(uint64(6))

	precision := pp.Precision()

	assert.Equal(t, uint64(6), precision)
}

// TestPublicParameters_CertificationDriver verifies retrieval of certification driver from public parameters
func TestPublicParameters_CertificationDriver(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.CertificationDriverReturns("my_certification_driver")

	certDriver := pp.CertificationDriver()

	assert.Equal(t, "my_certification_driver", certDriver)
}

// TestPublicParameters_GraphHiding verifies graph hiding setting from public parameters
func TestPublicParameters_GraphHiding(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.GraphHidingReturns(true)

	graphHiding := pp.GraphHiding()

	assert.True(t, graphHiding)
}

// TestPublicParameters_TokenDataHiding verifies token data hiding setting from public parameters
func TestPublicParameters_TokenDataHiding(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.TokenDataHidingReturns(false)

	tokenDataHiding := pp.TokenDataHiding()

	assert.False(t, tokenDataHiding)
}

// TestPublicParameters_MaxTokenValue verifies retrieval of maximum token value from public parameters
func TestPublicParameters_MaxTokenValue(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.MaxTokenValueReturns(uint64(1000))

	maxTokenValue := pp.MaxTokenValue()

	assert.Equal(t, uint64(1000), maxTokenValue)
}

// TestPublicParameters_Serialize verifies serialization of public parameters
func TestPublicParameters_Serialize(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.SerializeReturns([]byte("serialized_data"), nil)

	serializedData, err := pp.Serialize()

	require.NoError(t, err)
	assert.Equal(t, []byte("serialized_data"), serializedData)
}

// TestPublicParameters_Identifier verifies retrieval of identifier from public parameters
func TestPublicParameters_Identifier(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.TokenDriverNameReturns("my_identifier")

	identifier := pp.TokenDriverName()

	assert.Equal(t, driver.TokenDriverName("my_identifier"), identifier)
}

// TestPublicParameters_Auditors verifies retrieval of auditors list from public parameters
func TestPublicParameters_Auditors(t *testing.T) {
	pp := &PublicParameters{
		PublicParameters: &mock.PublicParameters{},
	}

	mockPP := pp.PublicParameters.(*mock.PublicParameters)
	mockPP.AuditorsReturns([]Identity{[]byte("auditor1"), []byte("auditor2")})

	auditors := pp.Auditors()

	expectedAuditors := []Identity{[]byte("auditor1"), []byte("auditor2")}
	assert.Equal(t, expectedAuditors, auditors)
}

// TestPublicParametersManager_PublicParameters verifies retrieval of public parameters from manager
func TestPublicParametersManager_PublicParameters(t *testing.T) {
	ppm := &PublicParametersManager{
		ppm: &mock.PublicParamsManager{},
	}
	pp := ppm.PublicParameters()
	assert.Nil(t, pp)

	ppm = &PublicParametersManager{
		ppm: &mock.PublicParamsManager{},
		pp: &PublicParameters{
			PublicParameters: &mock.PublicParameters{},
		},
	}
	pp = ppm.PublicParameters()
	assert.NotNil(t, pp)
}

// TestPublicParametersManager_PublicParameters_Nil verifies behavior when public parameters manager is nil
func TestPublicParametersManager_PublicParameters_Nil(t *testing.T) {
	ppm := &PublicParametersManager{
		ppm: &mock.PublicParamsManager{},
	}

	mockPPM := ppm.ppm.(*mock.PublicParamsManager)
	mockPPM.PublicParametersReturns(nil)
	pp := ppm.PublicParameters()
	assert.Nil(t, pp)
}

// TestPublicParametersManager_PublicParamsHash verifies public parameters hash retrieval
func TestPublicParametersManager_PublicParamsHash(t *testing.T) {
	mockPPM := &mock.PublicParamsManager{}
	expectedHash := PPHash("hash123")
	mockPPM.PublicParamsHashReturns(expectedHash)

	ppm := &PublicParametersManager{
		ppm: mockPPM,
	}

	hash := ppm.PublicParamsHash()
	assert.Equal(t, expectedHash, hash)
	assert.Equal(t, 1, mockPPM.PublicParamsHashCallCount())
}

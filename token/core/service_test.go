/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core_test

import (
	"encoding/json"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/core"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFactoryDirectory_PublicParametersFromBytes verifies the ability to unmarshal public parameters
// from bytes and handle cases where the driver is missing or the data is invalid.
func TestFactoryDirectory_PublicParametersFromBytes(t *testing.T) {
	name := driver.TokenDriverName("test-driver")
	version := driver.TokenDriverVersion(1)
	identifier := core.DriverIdentifier(name, version)

	ppmFactory := &mock.PPMFactory{}
	service := core.NewPPManagerFactoryService(core.NamedFactory[driver.PPMFactory]{
		Name:   identifier,
		Driver: ppmFactory,
	})

	ppBytes, err := json.Marshal(&pp.PublicParameters{
		Identifier: string(identifier),
	})
	require.NoError(t, err)

	// Success case: The driver is found and unmarshalling succeeds.
	expectedPP := &mock.PublicParameters{}
	ppmFactory.PublicParametersFromBytesReturns(expectedPP, nil)

	res, err := service.PublicParametersFromBytes(ppBytes)
	require.NoError(t, err)
	assert.Equal(t, expectedPP, res)

	// Driver not found case: The public parameters identifier does not match any registered factory.
	ppBytesUnknown, err := json.Marshal(&pp.PublicParameters{
		Identifier: "unknown-driver",
	})
	require.NoError(t, err)

	res, err = service.PublicParametersFromBytes(ppBytesUnknown)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "driver [unknown-driver] not found")

	// Unmarshal error case: The provided bytes are not valid JSON.
	res, err = service.PublicParametersFromBytes([]byte("invalid-json"))
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "failed deserializing public parameters")
}

// TestPPManagerFactoryService_NewPublicParametersManager verifies that a PublicParamsManager
// can be created for a registered driver and handles the case where the driver is not found.
func TestPPManagerFactoryService_NewPublicParametersManager(t *testing.T) {
	name := driver.TokenDriverName("test-driver")
	version := driver.TokenDriverVersion(1)
	identifier := core.DriverIdentifier(name, version)

	ppmFactory := &mock.PPMFactory{}
	service := core.NewPPManagerFactoryService(core.NamedFactory[driver.PPMFactory]{
		Name:   identifier,
		Driver: ppmFactory,
	})

	pp := &mock.PublicParameters{}
	pp.TokenDriverNameReturns(name)
	pp.TokenDriverVersionReturns(version)

	// Success case: A factory exists for the driver, and it successfully creates the manager.
	expectedPPM := &mock.PublicParamsManager{}
	ppmFactory.NewPublicParametersManagerReturns(expectedPPM, nil)

	res, err := service.NewPublicParametersManager(pp)
	require.NoError(t, err)
	assert.Equal(t, expectedPPM, res)

	// Driver not found case: No factory is registered for the specified driver identifier.
	ppUnknown := &mock.PublicParameters{}
	ppUnknown.TokenDriverNameReturns("unknown")
	ppUnknown.TokenDriverVersionReturns(1)

	res, err = service.NewPublicParametersManager(ppUnknown)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "driver [unknown.v1] not found")
}

// TestWalletServiceFactoryService_NewWalletService verifies that a WalletService can be created
// and handles various error conditions like public parameter unmarshalling failures and missing drivers.
func TestWalletServiceFactoryService_NewWalletService(t *testing.T) {
	name := driver.TokenDriverName("test-driver")
	version := driver.TokenDriverVersion(1)
	identifier := core.DriverIdentifier(name, version)

	walletFactory := &mock.WalletServiceFactory{}
	service := core.NewWalletServiceFactoryService(core.NamedFactory[driver.WalletServiceFactory]{
		Name:   identifier,
		Driver: walletFactory,
	})

	ppBytes, err := json.Marshal(&pp.PublicParameters{
		Identifier: string(identifier),
	})
	require.NoError(t, err)

	ppMock := &mock.PublicParameters{}
	ppMock.TokenDriverNameReturns(name)
	ppMock.TokenDriverVersionReturns(version)
	walletFactory.PublicParametersFromBytesReturns(ppMock, nil)

	tmsConfig := &mock.Configuration{}

	// Success case: Public parameters are valid and the wallet service is successfully created.
	expectedWS := &mock.WalletService{}
	walletFactory.NewWalletServiceReturns(expectedWS, nil)

	res, err := service.NewWalletService(tmsConfig, ppBytes)
	require.NoError(t, err)
	assert.Equal(t, expectedWS, res)

	// PublicParametersFromBytes error case: Fails to unmarshal public parameters.
	walletFactory.PublicParametersFromBytesReturns(nil, assert.AnError)
	res, err = service.NewWalletService(tmsConfig, ppBytes)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Equal(t, assert.AnError, err)

	// Driver not found case: Public parameters are unmarshalled, but no matching factory exists for the driver.
	walletFactory.PublicParametersFromBytesReturns(ppMock, nil)

	// Let's test the case where PublicParametersFromBytes succeeds but the driver is not in factories.
	ppUnknown := &mock.PublicParameters{}
	ppUnknown.TokenDriverNameReturns("unknown")
	ppUnknown.TokenDriverVersionReturns(1)

	walletFactory.PublicParametersFromBytesReturns(ppUnknown, nil)
	res, err = service.NewWalletService(tmsConfig, ppBytes)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "no validator found for token driver [unknown.v1]")
}

// TestTokenDriverService_NewTokenService verifies that a TokenManagerService can be created
// and handles failures in public parameter unmarshalling and missing driver registration.
func TestTokenDriverService_NewTokenService(t *testing.T) {
	name := driver.TokenDriverName("test-driver")
	version := driver.TokenDriverVersion(1)
	identifier := core.DriverIdentifier(name, version)

	driverMock := &mock.Driver{}
	service := core.NewTokenDriverService([]core.NamedFactory[driver.Driver]{
		{
			Name:   identifier,
			Driver: driverMock,
		},
	})

	ppBytes, err := json.Marshal(&pp.PublicParameters{
		Identifier: string(identifier),
	})
	require.NoError(t, err)

	ppMock := &mock.PublicParameters{}
	ppMock.TokenDriverNameReturns(name)
	ppMock.TokenDriverVersionReturns(version)
	driverMock.PublicParametersFromBytesReturns(ppMock, nil)

	tmsID := driver.TMSID{Network: "n1"}

	// Success case: TokenManagerService is successfully created.
	expectedTMS := &mock.TokenManagerService{}
	driverMock.NewTokenServiceReturns(expectedTMS, nil)

	res, err := service.NewTokenService(tmsID, ppBytes)
	require.NoError(t, err)
	assert.Equal(t, expectedTMS, res)

	// PublicParametersFromBytes error case: Unmarshalling public parameters fails.
	driverMock.PublicParametersFromBytesReturns(nil, assert.AnError)
	res, err = service.NewTokenService(tmsID, ppBytes)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Equal(t, assert.AnError, err)

	// Driver not found case: Public parameters are unmarshalled, but no driver factory matches the identifier.
	ppUnknown := &mock.PublicParameters{}
	ppUnknown.TokenDriverNameReturns("unknown")
	ppUnknown.TokenDriverVersionReturns(1)
	driverMock.PublicParametersFromBytesReturns(ppUnknown, nil)
	res, err = service.NewTokenService(tmsID, ppBytes)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "no token driver named 'unknown.v1' found")
}

// TestValidatorDriverService_NewDefaultValidator verifies retrieval of the default validator
// and proper error handling when the token driver is not found.
func TestValidatorDriverService_NewDefaultValidator(t *testing.T) {
	name := driver.TokenDriverName("test-driver")
	version := driver.TokenDriverVersion(1)
	identifier := core.DriverIdentifier(name, version)

	driverMock := &mock.ValidatorDriver{}
	service := core.NewValidatorDriverService(
		core.NamedFactory[driver.ValidatorDriver]{
			Name:   identifier,
			Driver: driverMock,
		},
	)

	pp := &mock.PublicParameters{}
	pp.TokenDriverNameReturns(name)
	pp.TokenDriverVersionReturns(version)

	// Success case: The default validator is successfully returned by the driver.
	expectedValidator := &mock.Validator{}
	driverMock.NewValidatorReturns(expectedValidator, nil)

	res, err := service.NewValidator(pp)
	require.NoError(t, err)
	assert.Equal(t, expectedValidator, res)

	// Driver not found case: No driver factory is registered for the specified identifier.
	ppUnknown := &mock.PublicParameters{}
	ppUnknown.TokenDriverNameReturns("unknown")
	ppUnknown.TokenDriverVersionReturns(1)

	res, err = service.NewValidator(ppUnknown)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "no validator found for token driver [unknown.v1]")
}

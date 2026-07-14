/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokenapi

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	mock2 "github.com/LFDT-Panurus/panurus/token/mock"
	"github.com/stretchr/testify/require"
)

// NewMockedManagementService returns a mocked token.ManagementService
func NewMockedManagementService(t *testing.T, tmsID token.TMSID) *token.ManagementService {
	t.Helper()
	tms := &mock.TokenManagerService{}
	pp := &mock.PublicParameters{}
	ppm := &mock.PublicParamsManager{}
	ppm.PublicParametersReturns(pp)
	tms.PublicParamsManagerReturns(ppm)
	vp := &mock2.VaultProvider{}
	vault := &mock.Vault{}
	qe := &mock.QueryEngine{}
	vault.QueryEngineReturns(qe)
	vp.VaultReturns(vault, nil)

	res, err := token.NewManagementService(tmsID, tms, nil, vp, nil, nil)
	require.NoError(t, err)

	return res
}

// NewMockedManagementServiceWithValidation returns a mocked token.ManagementService and a validator
func NewMockedManagementServiceWithValidation(t *testing.T, tmsID token.TMSID) (*token.ManagementService, *mock.Validator) {
	t.Helper()
	tms := &mock.TokenManagerService{}
	pp := &mock.PublicParameters{}
	ppm := &mock.PublicParamsManager{}
	ppm.PublicParametersReturns(pp)
	tms.PublicParamsManagerReturns(ppm)
	vp := &mock2.VaultProvider{}
	vault := &mock.Vault{}
	qe := &mock.QueryEngine{}
	vault.QueryEngineReturns(qe)
	vp.VaultReturns(vault, nil)
	validator := &mock.Validator{}
	tms.ValidatorReturns(validator, nil)

	res, err := token.NewManagementService(tmsID, tms, nil, vp, nil, nil)
	require.NoError(t, err)

	return res, validator
}

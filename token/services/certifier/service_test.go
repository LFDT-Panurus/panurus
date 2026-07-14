/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package certifier

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/certifier/driver/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test calling Start() on a CertificationService
func TestCertificationService_Start_Success(t *testing.T) {
	mockService := &mock.CertificationService{}
	mockService.StartReturns(nil)

	service := &CertificationService{c: mockService}

	err := service.Start()
	require.NoError(t, err, "Start should succeed when underlying service succeeds")

	// Verify the mock was called once
	assert.Equal(t, 1, mockService.StartCallCount())
}

// Test failure when calling a failing Start() on a CertificationService
func TestCertificationService_Start_Error(t *testing.T) {
	expectedErr := errors.New("start failed")
	mockService := &mock.CertificationService{}
	mockService.StartReturns(expectedErr)

	service := &CertificationService{c: mockService}

	err := service.Start()
	require.Error(t, err)
	require.ErrorIs(t, err, expectedErr)

	// Verify the mock was called once
	assert.Equal(t, 1, mockService.StartCallCount())
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package certifier

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/services/certifier/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test calling IsCertified
func TestCertificationClient_IsCertified(t *testing.T) {
	mockClient := &mock.CertificationClient{}
	mockClient.IsCertifiedReturns(true)

	client := &CertificationClient{c: mockClient}

	id := &token.ID{
		TxId:  "tx1",
		Index: 0,
	}

	result := client.IsCertified(context.Background(), id)
	assert.True(t, result)

	// Verify the mock was called with correct arguments
	assert.Equal(t, 1, mockClient.IsCertifiedCallCount())
	ctx, calledID := mockClient.IsCertifiedArgsForCall(0)
	assert.NotNil(t, ctx)
	assert.Equal(t, id, calledID)
}

// Test negative response from Certifier
func TestCertificationClient_IsCertified_False(t *testing.T) {
	mockClient := &mock.CertificationClient{}
	mockClient.IsCertifiedReturns(false)

	client := &CertificationClient{c: mockClient}

	id := &token.ID{
		TxId:  "tx2",
		Index: 1,
	}

	result := client.IsCertified(context.Background(), id)
	assert.False(t, result)

	assert.Equal(t, 1, mockClient.IsCertifiedCallCount())
}

// Test calling RequestCertification
func TestCertificationClient_RequestCertification_Success(t *testing.T) {
	mockClient := &mock.CertificationClient{}
	mockClient.RequestCertificationReturns(nil)

	client := &CertificationClient{c: mockClient}

	ids := []*token.ID{
		{TxId: "tx1", Index: 0},
		{TxId: "tx2", Index: 1},
	}

	err := client.RequestCertification(context.Background(), ids...)
	require.NoError(t, err, "RequestCertification should succeed when underlying client succeeds")

	// Verify the mock was called
	assert.Equal(t, 1, mockClient.RequestCertificationCallCount())
	ctx, calledIDs := mockClient.RequestCertificationArgsForCall(0)
	assert.NotNil(t, ctx)
	assert.Equal(t, ids, calledIDs)
}

// Test failing with a certifier that returns an error
func TestCertificationClient_RequestCertification_Error(t *testing.T) {
	expectedErr := errors.New("certification failed")
	mockClient := &mock.CertificationClient{}
	mockClient.RequestCertificationReturns(expectedErr)

	client := &CertificationClient{c: mockClient}

	ids := []*token.ID{
		{TxId: "tx1", Index: 0},
	}

	err := client.RequestCertification(context.Background(), ids...)
	require.Error(t, err)
	require.ErrorIs(t, err, expectedErr)

	// Verify the mock was called once
	assert.Equal(t, 1, mockClient.RequestCertificationCallCount())
}

// Test failing when RequestCertification is called with an empty id list
func TestCertificationClient_RequestCertification_NoIDs(t *testing.T) {
	mockClient := &mock.CertificationClient{}
	mockClient.RequestCertificationReturns(nil)

	client := &CertificationClient{c: mockClient}

	err := client.RequestCertification(context.Background())
	require.NoError(t, err, "RequestCertification should handle empty ID list")

	// Verify the mock was called once with empty slice
	assert.Equal(t, 1, mockClient.RequestCertificationCallCount())
	ctx, calledIDs := mockClient.RequestCertificationArgsForCall(0)
	assert.NotNil(t, ctx)
	assert.Empty(t, calledIDs)
}

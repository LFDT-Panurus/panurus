/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dummy

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constructing a new Certifier Driver
func TestNewDriver(t *testing.T) {
	driver := NewDriver()
	assert.NotNil(t, driver)
}

// Test constructing a new Certifier dummy (test) Client
func TestDriver_NewCertificationClient(t *testing.T) {
	driver := NewDriver()

	client, err := driver.NewCertificationClient(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, client)

	// Verify it's the dummy client
	dummyClient, ok := client.(*CertificationClient)
	assert.True(t, ok)
	assert.NotNil(t, dummyClient)
}

// Test constructing a new dummy (test) Certifier Service
func TestDriver_NewCertificationService(t *testing.T) {
	driver := NewDriver()

	service, err := driver.NewCertificationService(nil, "test-wallet")
	require.NoError(t, err)
	assert.NotNil(t, service)

	// Verify it's the dummy service
	dummyService, ok := service.(*CertificationService)
	assert.True(t, ok)
	assert.NotNil(t, dummyService)
}

// Test that the dummy (test) client returns true in calls to IsCertified
func TestCertificationClient_IsCertified(t *testing.T) {
	client := &CertificationClient{}

	id := &token.ID{
		TxId:  "tx1",
		Index: 0,
	}

	// Dummy client always returns true
	certified := client.IsCertified(context.Background(), id)
	assert.True(t, certified, "dummy client should always return true for IsCertified")
}

// Test that the dummy (test) client succeeds to RequestCertification for given ids
func TestCertificationClient_RequestCertification(t *testing.T) {
	client := &CertificationClient{}

	ids := []*token.ID{
		{TxId: "tx1", Index: 0},
		{TxId: "tx2", Index: 1},
	}

	// Dummy client always succeeds
	err := client.RequestCertification(context.Background(), ids...)
	require.NoError(t, err, "dummy client should always succeed")
}

// Test that the dummy (test) client doesn't fail to RequestCertification for an empty id list
func TestCertificationClient_RequestCertification_NoIDs(t *testing.T) {
	client := &CertificationClient{}

	// Should handle empty list
	err := client.RequestCertification(context.Background())
	require.NoError(t, err, "dummy client should handle empty ID list")
}

// Test that the dummy (test) client can Start()
func TestCertificationClient_Start(t *testing.T) {
	client := &CertificationClient{}

	// Dummy client start does nothing
	err := client.Start()
	require.NoError(t, err, "dummy client Start should always succeed")
}

// Test that the dummy (test) service can Start()
func TestCertificationService_Start(t *testing.T) {
	service := &CertificationService{}

	// Dummy service start does nothing
	err := service.Start()
	require.NoError(t, err, "dummy service Start should always succeed")
}

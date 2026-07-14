/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package certifier

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.Driver for testing
type mockDriver struct{}

func (m *mockDriver) NewCertificationClient(_ context.Context, _ *token.ManagementService) (driver.CertificationClient, error) {
	return nil, nil
}

func (m *mockDriver) NewCertificationService(_ *token.ManagementService, _ string) (driver.CertificationService, error) {
	return nil, nil
}

func TestHolder_Register_And_Get(t *testing.T) {
	d := &mockDriver{}
	key := "test-driver-register"

	holder.Register(key, d)

	got, ok := holder.Get(key)
	require.True(t, ok)
	assert.Equal(t, d, got)
}

func TestHolder_Get_NonExistent(t *testing.T) {
	_, ok := holder.Get("non-existent-driver")
	assert.False(t, ok)
}

func TestHolder_Register_NilDriver_Panics(t *testing.T) {
	assert.Panics(t, func() {
		holder.Register("nil-driver", nil)
	})
}

func TestHolder_Register_Duplicate_Panics(t *testing.T) {
	d := &mockDriver{}
	key := "test-driver-duplicate"

	holder.Register(key, d)

	assert.Panics(t, func() {
		holder.Register(key, d)
	})
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	drivermock "github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServicePrecisionFollowsPublicParamsUpdate checks that the selectors use the precision of
// the current public parameters. After an update the TMS is rebuilt, but the service hands back
// the manager it cached for the TMS when the parameters had a lower precision.
func TestServicePrecisionFollowsPublicParamsUpdate(t *testing.T) {
	svc := NewService(&fixedLockerProvider{locker: &recordingLocker{lockFailAfter: 100}}, &emptyConfigProvider{})

	before, err := svc.SelectorManager(newTMSWithPrecision(t, 32))
	require.NoError(t, err)

	after, err := svc.SelectorManager(newTMSWithPrecision(t, 64))
	require.NoError(t, err)
	assert.Same(t, before, after, "the manager is cached by TMS")

	s, err := after.NewSelector("tx1")
	require.NoError(t, err)
	sel, ok := s.(*selector)
	require.True(t, ok)
	assert.Equal(t, uint64(64), sel.precision)
}

// newTMSWithPrecision returns a management service for the same TMS every time, whose public
// parameters have the given precision.
func newTMSWithPrecision(t *testing.T, precision uint64) *token.ManagementService {
	t.Helper()

	driverTMS := &drivermock.TokenManagerService{}
	ppm := &drivermock.PublicParamsManager{}
	pp := &drivermock.PublicParameters{}
	pp.PrecisionReturns(precision)
	ppm.PublicParametersReturns(pp)
	driverTMS.PublicParamsManagerReturns(ppm)

	tms, err := token.NewManagementService(
		token.TMSID{Network: "n1", Channel: "c1", Namespace: "ns1"},
		driverTMS,
		nil,
		&mockVaultProvider{},
		nil,
		nil,
	)
	require.NoError(t, err)

	return tms
}

// mockVaultProvider is the minimal VaultProvider NewManagementService needs.
type mockVaultProvider struct{}

func (v *mockVaultProvider) Vault(string, string, string) (driver.Vault, error) {
	return &drivermock.Vault{}, nil
}

// emptyConfigProvider is a ConfigProvider with no selector configuration, so defaults apply.
type emptyConfigProvider struct{}

func (c *emptyConfigProvider) UnmarshalKey(string, any) error { return nil }

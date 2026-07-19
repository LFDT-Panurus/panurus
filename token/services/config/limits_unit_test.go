/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config_test

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/config/mocks"
	fscconfig "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceLimitsProvider_Unset(t *testing.T) {
	cp := &mocks.Provider{}
	cp.IsSetReturns(false)

	p := config.NewResourceLimitsProvider(cp)
	limits, err := p.ResourceLimits()
	require.NoError(t, err)
	assert.Equal(t, driver.DefaultResourceLimits(), limits)
}

func TestResourceLimitsProvider_PartialOverride(t *testing.T) {
	cp, err := fscconfig.NewProvider("./testdata/token0")
	require.NoError(t, err)
	require.NoError(t, cp.MergeConfig([]byte("token:\n  validation:\n    limits:\n      maxActions: 2\n      maxProofBytes: 1024\n")))

	p := config.NewResourceLimitsProvider(cp)
	limits, err := p.ResourceLimits()
	require.NoError(t, err)

	want := driver.DefaultResourceLimits()
	want.MaxActions = 2
	want.MaxProofBytes = 1024
	assert.Equal(t, want, limits)
}

func TestResourceLimitsProvider_UnmarshalError(t *testing.T) {
	cp := &mocks.Provider{}
	cp.IsSetReturns(true)
	cp.UnmarshalKeyReturns(assert.AnError)

	p := config.NewResourceLimitsProvider(cp)
	_, err := p.ResourceLimits()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid configuration")
}

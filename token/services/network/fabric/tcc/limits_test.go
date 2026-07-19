/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"strconv"
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvResourceLimitsProvider_Unset(t *testing.T) {
	p := &EnvResourceLimitsProvider{Getenv: func(string) string { return "" }}
	limits, err := p.ResourceLimits()
	require.NoError(t, err)
	assert.Equal(t, driver.DefaultResourceLimits(), limits)
}

func TestEnvResourceLimitsProvider_PartialOverride(t *testing.T) {
	env := map[string]string{
		EnvMaxActions:    "2",
		EnvMaxProofBytes: "1024",
	}
	p := &EnvResourceLimitsProvider{Getenv: func(key string) string { return env[key] }}

	limits, err := p.ResourceLimits()
	require.NoError(t, err)

	want := driver.DefaultResourceLimits()
	want.MaxActions = 2
	want.MaxProofBytes = 1024
	assert.Equal(t, want, limits)
}

func TestEnvResourceLimitsProvider_AllOverridden(t *testing.T) {
	env := map[string]string{
		EnvMaxRequestBytes:       "1",
		EnvMaxActions:            "2",
		EnvMaxSignatures:         "3",
		EnvMaxSignatureBytes:     "4",
		EnvMaxActionBytes:        "5",
		EnvMaxInputs:             "6",
		EnvMaxOutputs:            "7",
		EnvMaxMetadataEntries:    "8",
		EnvMaxMetadataKeyBytes:   "9",
		EnvMaxMetadataValueBytes: "10",
		EnvMaxProofBytes:         "11",
	}
	p := &EnvResourceLimitsProvider{Getenv: func(key string) string { return env[key] }}

	limits, err := p.ResourceLimits()
	require.NoError(t, err)
	assert.Equal(t, driver.ResourceLimits{
		MaxRequestBytes:       1,
		MaxActions:            2,
		MaxSignatures:         3,
		MaxSignatureBytes:     4,
		MaxActionBytes:        5,
		MaxInputs:             6,
		MaxOutputs:            7,
		MaxMetadataEntries:    8,
		MaxMetadataKeyBytes:   9,
		MaxMetadataValueBytes: 10,
		MaxProofBytes:         11,
	}, limits)
}

func TestEnvResourceLimitsProvider_InvalidValue(t *testing.T) {
	p := &EnvResourceLimitsProvider{Getenv: func(key string) string {
		if key == EnvMaxActions {
			return "not-a-number"
		}

		return ""
	}}

	_, err := p.ResourceLimits()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvMaxActions)
	var numErr *strconv.NumError
	assert.ErrorAs(t, err, &numErr)
}

func TestNewEnvResourceLimitsProvider_DefaultsToOsGetenv(t *testing.T) {
	p := NewEnvResourceLimitsProvider()
	require.NotNil(t, p.Getenv)
	limits, err := p.ResourceLimits()
	require.NoError(t, err)
	assert.Equal(t, driver.DefaultResourceLimits(), limits)
}

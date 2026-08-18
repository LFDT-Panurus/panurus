/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tcc

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryLimits_WithDefaults_AllUnset(t *testing.T) {
	var l QueryLimits
	assert.Equal(t, DefaultQueryLimits(), l.WithDefaults())
}

func TestQueryLimits_WithDefaults_PartialOverride(t *testing.T) {
	l := QueryLimits{MaxQueryItems: 8}

	want := DefaultQueryLimits()
	want.MaxQueryItems = 8
	assert.Equal(t, want, l.WithDefaults())
}

func TestQueryLimits_WithDefaults_FullyOverridden(t *testing.T) {
	l := QueryLimits{MaxQueryRequestBytes: 16, MaxQueryItems: 8}
	assert.Equal(t, l, l.WithDefaults())
}

func TestQueryLimits_WithDefaults_NegativeValuesFallBackToDefault(t *testing.T) {
	l := QueryLimits{MaxQueryRequestBytes: -1, MaxQueryItems: -1}
	assert.Equal(t, DefaultQueryLimits(), l.WithDefaults())
}

func TestQueryLimits_CheckRequestSize(t *testing.T) {
	l := DefaultQueryLimits()

	t.Run("below limit", func(t *testing.T) {
		require.NoError(t, l.CheckRequestSize(make([]byte, l.MaxQueryRequestBytes-1)))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, l.CheckRequestSize(make([]byte, l.MaxQueryRequestBytes)))
	})
	t.Run("above limit", func(t *testing.T) {
		require.ErrorIs(t, l.CheckRequestSize(make([]byte, l.MaxQueryRequestBytes+1)), ErrQueryRequestTooLarge)
	})
}

func TestQueryLimits_CheckRequestSize_CustomLimit(t *testing.T) {
	l := QueryLimits{MaxQueryRequestBytes: 16}.WithDefaults()

	require.NoError(t, l.CheckRequestSize(make([]byte, 16)))
	require.ErrorIs(t, l.CheckRequestSize(make([]byte, 17)), ErrQueryRequestTooLarge)
}

func TestQueryLimits_CheckItemCount(t *testing.T) {
	l := DefaultQueryLimits()

	t.Run("below limit", func(t *testing.T) {
		require.NoError(t, l.CheckItemCount(l.MaxQueryItems-1))
	})
	t.Run("at limit", func(t *testing.T) {
		require.NoError(t, l.CheckItemCount(l.MaxQueryItems))
	})
	t.Run("above limit", func(t *testing.T) {
		require.ErrorIs(t, l.CheckItemCount(l.MaxQueryItems+1), ErrTooManyQueryItems)
	})
}

func TestQueryLimits_CheckItemCount_CustomLimit(t *testing.T) {
	l := QueryLimits{MaxQueryItems: 3}.WithDefaults()

	require.NoError(t, l.CheckItemCount(3))
	require.ErrorIs(t, l.CheckItemCount(4), ErrTooManyQueryItems)
}

// A full MaxQueryItems batch of realistic state keys must fit within MaxQueryRequestBytes, so the
// item cap — not the byte cap — is the limit callers hit first.
func TestDefaultQueryLimits_ByteCapDoesNotShadowItemCap(t *testing.T) {
	l := DefaultQueryLimits()
	const realisticKeyBytes = 128 // a token output key, JSON-quoted, plus a separator

	assert.Greater(t, l.MaxQueryRequestBytes, l.MaxQueryItems*realisticKeyBytes)
}

func TestEnvQueryLimitsProvider_Unset(t *testing.T) {
	p := &EnvQueryLimitsProvider{Getenv: func(string) string { return "" }}

	limits, err := p.QueryLimits()
	require.NoError(t, err)
	assert.Equal(t, DefaultQueryLimits(), limits)
}

func TestEnvQueryLimitsProvider_PartialOverride(t *testing.T) {
	env := map[string]string{EnvMaxQueryItems: "8"}
	p := &EnvQueryLimitsProvider{Getenv: func(key string) string { return env[key] }}

	limits, err := p.QueryLimits()
	require.NoError(t, err)

	want := DefaultQueryLimits()
	want.MaxQueryItems = 8
	assert.Equal(t, want, limits)
}

func TestEnvQueryLimitsProvider_AllOverridden(t *testing.T) {
	env := map[string]string{
		EnvMaxQueryRequestBytes: "1",
		EnvMaxQueryItems:        "2",
	}
	p := &EnvQueryLimitsProvider{Getenv: func(key string) string { return env[key] }}

	limits, err := p.QueryLimits()
	require.NoError(t, err)
	assert.Equal(t, QueryLimits{MaxQueryRequestBytes: 1, MaxQueryItems: 2}, limits)
}

func TestEnvQueryLimitsProvider_InvalidValue(t *testing.T) {
	p := &EnvQueryLimitsProvider{Getenv: func(key string) string {
		if key == EnvMaxQueryItems {
			return "not-a-number"
		}

		return ""
	}}

	_, err := p.QueryLimits()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvMaxQueryItems)
	var numErr *strconv.NumError
	assert.ErrorAs(t, err, &numErr)
}

func TestNewEnvQueryLimitsProvider_DefaultsToOsGetenv(t *testing.T) {
	p := NewEnvQueryLimitsProvider()
	require.NotNil(t, p.Getenv)

	limits, err := p.QueryLimits()
	require.NoError(t, err)
	assert.Equal(t, DefaultQueryLimits(), limits)
}

func TestTokenChaincode_EffectiveQueryLimits(t *testing.T) {
	t.Run("unconfigured chaincode falls back to defaults", func(t *testing.T) {
		cc := &TokenChaincode{}
		assert.Equal(t, DefaultQueryLimits(), cc.effectiveQueryLimits())
	})
	t.Run("configured limits are honoured", func(t *testing.T) {
		cc := &TokenChaincode{QueryLimits: QueryLimits{MaxQueryItems: 2}}
		assert.Equal(t, 2, cc.effectiveQueryLimits().MaxQueryItems)
		assert.Equal(t, DefaultQueryLimits().MaxQueryRequestBytes, cc.effectiveQueryLimits().MaxQueryRequestBytes)
	})
}

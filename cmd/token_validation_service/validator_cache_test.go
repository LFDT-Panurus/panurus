/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package bench

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// paramsWithMissingPublicParams points at a directory with no params.txt, so
// validator initialization fails.
func paramsWithMissingPublicParams(t *testing.T) *transferServiceParams {
	t.Helper()

	return &transferServiceParams{
		OutputPath: filepath.Join(t.TempDir(), "transfers_i2_o2", "0"),
		TokenData:  &TokenData{TokenRequestRaw: []byte("irrelevant"), TxID: "tx"},
	}
}

// TestValidatorCacheGetReportsErrorOnEveryCall checks that a failed init is
// reported to every caller and not memoized.
func TestValidatorCacheGetReportsErrorOnEveryCall(t *testing.T) {
	params := paramsWithMissingPublicParams(t)
	cache := &validatorCache{}

	for i := range 3 {
		validator, err := cache.get(params)
		require.Errorf(t, err, "call %d should report the initialization error", i)
		require.Nilf(t, validator, "call %d should not return a validator", i)
	}
}

// TestNewViewReportsValidatorErrorOnEveryCall is the regression test for #2565:
// every NewView call must fail, not just the first.
func TestNewViewReportsValidatorErrorOnEveryCall(t *testing.T) {
	in, err := json.Marshal(paramsWithMissingPublicParams(t))
	require.NoError(t, err)

	f := &TokenValidationServiceViewFactory{}
	for i := range 3 {
		v, err := f.NewView(in)
		require.Errorf(t, err, "NewView call %d should report the initialization error", i)
		require.Contains(t, err.Error(), "failed to create token validator")
		require.Nilf(t, v, "NewView call %d should not return a view", i)
	}
}

// TestNewViewSharesOneValidator checks that every view shares one validator.
func TestNewViewSharesOneValidator(t *testing.T) {
	params, err := NewTokenValidationParamsSlice(DefaultTestRoot)
	if err != nil {
		t.Skipf("test data not available: %v", err)
	}
	require.NotEmpty(t, params)

	in, err := json.Marshal(params[0])
	require.NoError(t, err)

	f := &TokenValidationServiceViewFactory{}

	first, err := f.NewView(in)
	require.NoError(t, err)
	second, err := f.NewView(in)
	require.NoError(t, err)

	firstView, ok := first.(*TokenValidationServiceView)
	require.True(t, ok)
	secondView, ok := second.(*TokenValidationServiceView)
	require.True(t, ok)

	require.NotNil(t, firstView.validator)
	require.Same(t, firstView.validator, secondView.validator)
}

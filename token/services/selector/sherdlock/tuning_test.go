/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTuning_NormalizeSubstitutesDefaults verifies every non-positive field, including
// negative ones, falls back to its package default, and that positive values are preserved.
func TestTuning_NormalizeSubstitutesDefaults(t *testing.T) {
	assert.Equal(t, Tuning{
		SufficiencyWindow:   defaultSufficiencyWindow,
		MaxSufficiencyRatio: defaultMaxSufficiencyRatio,
		MaxImmediateRetries: defaultMaxImmediateRetries,
		MaxBatchWindow:      defaultMaxBatchWindow,
	}, Tuning{}.normalize(), "the zero Tuning must normalize to the package defaults")

	assert.Equal(t, Tuning{
		SufficiencyWindow:   defaultSufficiencyWindow,
		MaxSufficiencyRatio: defaultMaxSufficiencyRatio,
		MaxImmediateRetries: defaultMaxImmediateRetries,
		MaxBatchWindow:      defaultMaxBatchWindow,
	}, Tuning{
		SufficiencyWindow:   -1,
		MaxSufficiencyRatio: -1,
		MaxImmediateRetries: -1,
		MaxBatchWindow:      -1,
	}.normalize(), "negative fields must fall back to the default just like zero")

	assert.Equal(t, Tuning{
		SufficiencyWindow:   1,
		MaxSufficiencyRatio: 2,
		MaxImmediateRetries: 3,
		MaxBatchWindow:      4,
	}, Tuning{
		SufficiencyWindow:   1,
		MaxSufficiencyRatio: 2,
		MaxImmediateRetries: 3,
		MaxBatchWindow:      4,
	}.normalize(), "positive fields must be preserved")
}

// TestTuning_NormalizeClampsMaxBatchWindow verifies a MaxBatchWindow above MaxBatchWindowLimit
// is clamped down to it, rather than let through to the store.
func TestTuning_NormalizeClampsMaxBatchWindow(t *testing.T) {
	got := Tuning{MaxBatchWindow: MaxBatchWindowLimit + 1}.normalize()
	assert.Equal(t, MaxBatchWindowLimit, got.MaxBatchWindow)

	got = Tuning{MaxBatchWindow: MaxBatchWindowLimit}.normalize()
	assert.Equal(t, MaxBatchWindowLimit, got.MaxBatchWindow, "the limit itself must not be clamped away")
}

// TestCompileOptions_UnderliesNormalizationChokepoint verifies CompileOptions - the entry point
// every construction path (NewSelector, NewManager via WithTuning) funnels through - always
// returns a normalized Tuning, even when every option is a no-op or absent, so normalization
// cannot be bypassed by any caller.
func TestCompileOptions_UnderliesNormalizationChokepoint(t *testing.T) {
	assert.Equal(t, Tuning{
		SufficiencyWindow:   defaultSufficiencyWindow,
		MaxSufficiencyRatio: defaultMaxSufficiencyRatio,
		MaxImmediateRetries: defaultMaxImmediateRetries,
		MaxBatchWindow:      defaultMaxBatchWindow,
	}, CompileOptions())

	assert.Equal(t, Tuning{
		SufficiencyWindow:   defaultSufficiencyWindow,
		MaxSufficiencyRatio: defaultMaxSufficiencyRatio,
		MaxImmediateRetries: defaultMaxImmediateRetries,
		MaxBatchWindow:      defaultMaxBatchWindow,
	}, CompileOptions(WithTuning(Tuning{})), "WithTuning(zero Tuning) must still be normalized")

	assert.Equal(t, Tuning{
		SufficiencyWindow:   7,
		MaxSufficiencyRatio: defaultMaxSufficiencyRatio,
		MaxImmediateRetries: defaultMaxImmediateRetries,
		MaxBatchWindow:      defaultMaxBatchWindow,
	}, CompileOptions(WithSufficiencyWindow(7)))

	assert.Equal(t, Tuning{
		SufficiencyWindow:   defaultSufficiencyWindow,
		MaxSufficiencyRatio: defaultMaxSufficiencyRatio,
		MaxImmediateRetries: defaultMaxImmediateRetries,
		MaxBatchWindow:      defaultMaxBatchWindow,
	}, CompileOptions(nil), "a nil option must be ignored, not panic")
}

// TestCompileOptions_LastOptionWins verifies a later option overrides an earlier one for any
// field it sets, mirroring the convention in ratelimit.CompileOptions.
func TestCompileOptions_LastOptionWins(t *testing.T) {
	got := CompileOptions(WithSufficiencyWindow(2), WithSufficiencyWindow(9))
	assert.Equal(t, 9, got.SufficiencyWindow)

	got = CompileOptions(WithTuning(Tuning{SufficiencyWindow: 2}), WithSufficiencyWindow(9))
	assert.Equal(t, 9, got.SufficiencyWindow)
}

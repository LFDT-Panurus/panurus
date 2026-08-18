/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ratelimit

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConfiguration is a Configuration built from explicit values, standing in for the parsed
// token.selector configuration.
type fakeConfiguration struct {
	enabled    bool
	rate       float64
	burst      int
	maxBuckets int
}

func (c *fakeConfiguration) IsRateLimitEnabled() bool    { return c.enabled }
func (c *fakeConfiguration) GetRateLimit() float64       { return c.rate }
func (c *fakeConfiguration) GetRateLimitBurst() int      { return c.burst }
func (c *fakeConfiguration) GetRateLimitMaxBuckets() int { return c.maxBuckets }

// TestOptions_DisabledByDefault verifies that without options and without configuration there is no
// limiter at all.
func TestOptions_DisabledByDefault(t *testing.T) {
	assert.Nil(t, CompileOptions().Limiter(&fakeConfiguration{}))
	assert.Nil(t, CompileOptions().Limiter(nil))
}

// TestOptions_FromConfiguration verifies an enabled configuration produces a limiter with the
// configured rate and burst.
func TestOptions_FromConfiguration(t *testing.T) {
	limiter := CompileOptions().Limiter(&fakeConfiguration{enabled: true, rate: 2, burst: 1})
	require.NotNil(t, limiter)

	ctx := context.Background()
	require.NoError(t, limiter.Allow(ctx, testScope, "alice"))
	require.ErrorIs(t, limiter.Allow(ctx, testScope, "alice"), token.SelectorRateLimited)
}

// TestOptions_WithLimiterOverridesConfiguration verifies an explicitly supplied limiter wins over
// the configuration.
func TestOptions_WithLimiterOverridesConfiguration(t *testing.T) {
	supplied := &countingLimiter{}

	limiter := CompileOptions(WithLimiter(supplied)).Limiter(&fakeConfiguration{enabled: true, rate: 100})
	require.Same(t, supplied, limiter)
}

// TestOptions_WithNilLimiterDisables verifies WithLimiter(nil) switches rate limiting off even when
// the configuration enables it.
func TestOptions_WithNilLimiterDisables(t *testing.T) {
	limiter := CompileOptions(WithLimiter(nil)).Limiter(&fakeConfiguration{enabled: true, rate: 100})
	assert.Nil(t, limiter)
}

// TestOptions_WithDefaultLimiter verifies the built-in limiter can be enabled from code alone, with
// the documented default burst of 200 requests.
func TestOptions_WithDefaultLimiter(t *testing.T) {
	limiter := CompileOptions(WithDefaultLimiter()).Limiter(&fakeConfiguration{})
	require.NotNil(t, limiter)

	bucketLimiter, ok := limiter.(*BucketLimiter)
	require.True(t, ok)
	assert.InDelta(t, DefaultRate, bucketLimiter.rate, 0)
	assert.InDelta(t, 200.0, bucketLimiter.burst, 0)
}

// TestOptions_LastOptionWins verifies options are applied in order.
func TestOptions_LastOptionWins(t *testing.T) {
	supplied := &countingLimiter{}

	assert.Nil(t, CompileOptions(WithLimiter(supplied), WithLimiter(nil)).Limiter(nil))
	assert.Same(t, supplied, CompileOptions(WithLimiter(nil), WithLimiter(supplied)).Limiter(nil))
}

// TestOptions_NilOptionIsIgnored verifies a nil Option does not panic, so a caller assembling
// options conditionally does not have to filter them.
func TestOptions_NilOptionIsIgnored(t *testing.T) {
	assert.Nil(t, CompileOptions(nil).Limiter(&fakeConfiguration{}))
}

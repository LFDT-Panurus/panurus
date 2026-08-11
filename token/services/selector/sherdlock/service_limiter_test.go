/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/config"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover which limiter NewService installs. Whether that limiter is then
// enforced on selections is covered by TestService_SelectionIsMeteredByTheLimiter and by
// the ratelimit package's own tests.

// stubConfigProvider fills the selector config with apply, leaving every other key at its
// default.
type stubConfigProvider struct {
	apply func(*config.Config)
}

func (s *stubConfigProvider) UnmarshalKey(_ string, rawVal any) error {
	if s.apply == nil {
		return nil
	}
	cfg, ok := rawVal.(*config.Config)
	if !ok {
		return nil
	}
	s.apply(cfg)

	return nil
}

// newLimiterService builds a service with just enough wiring to inspect its limiter.
func newLimiterService(t *testing.T, cp ConfigProvider, opts ...Option) *SelectorService {
	t.Helper()

	svc := NewService(nil, nil, cp, &disabled.Provider{}, opts...)
	t.Cleanup(svc.Shutdown)

	return svc
}

// exhaust consumes n requests for wallet, requiring all of them to be allowed, and then
// requires the next one to be throttled. It is how a limiter's effective budget is read
// back without depending on its internals.
func exhaust(t *testing.T, limiter ratelimit.Limiter, wallet string, n int) {
	t.Helper()

	for i := range n {
		require.NoErrorf(t, limiter.Allow(wallet), "request %d of %d was denied", i+1, n)
	}
	require.ErrorIs(t, limiter.Allow(wallet), token.SelectorRateLimited)
}

// With no option and no configuration, nothing is throttled: an application that has not
// asked for rate limiting must behave exactly as it did before the limiter existed.
func TestNewService_InstallsNoLimiterByDefault(t *testing.T) {
	svc := newLimiterService(t, &stubConfigProvider{})

	assert.Nil(t, svc.limiter, "rate limiting must be off unless it is asked for")
}

// rateLimitEnabled is the documented way to switch the built-in limiter on without picking
// any numbers.
func TestNewService_ConfigCanEnableTheBuiltInLimiter(t *testing.T) {
	svc := newLimiterService(t, &stubConfigProvider{apply: func(c *config.Config) {
		c.RateLimitEnabled = true
	}})

	require.NotNil(t, svc.limiter)
	assert.IsType(t, &ratelimit.TokenBucketRateLimiter{}, svc.limiter)
	exhaust(t, svc.limiter, "alice", ratelimit.DefaultBurst)
}

// A configured rate switches the limiter on by itself: a deployment that writes rateLimit
// and nothing else wants throttling, and silently ignoring the value would be the worse
// surprise.
func TestNewService_HonoursConfiguredRateLimit(t *testing.T) {
	svc := newLimiterService(t, &stubConfigProvider{apply: func(c *config.Config) {
		c.RateLimit = 2
		c.RateLimitBurst = 3
	}})

	require.NotNil(t, svc.limiter)
	exhaust(t, svc.limiter, "alice", 3)
}

// A negative rate is the explicit "off", and outranks rateLimitEnabled.
func TestNewService_NegativeRateLimitOverridesTheEnabledFlag(t *testing.T) {
	svc := newLimiterService(t, &stubConfigProvider{apply: func(c *config.Config) {
		c.RateLimitEnabled = true
		c.RateLimit = -1
	}})

	assert.Nil(t, svc.limiter)
}

// An application-supplied limiter overrides whatever the configuration selects, and an
// explicit nil keeps rate limiting off for applications that throttle inside their own
// Locker instead.
func TestNewService_WithLimiterOverridesTheConfiguration(t *testing.T) {
	// Configuration that switches the built-in limiter on, so the option has something to
	// override rather than agreeing with the default.
	enabled := &stubConfigProvider{apply: func(c *config.Config) { c.RateLimitEnabled = true }}

	t.Run("custom limiter", func(t *testing.T) {
		custom := ratelimit.NewTokenBucketRateLimiter(1, 1, 0)
		t.Cleanup(custom.Stop)

		svc := newLimiterService(t, enabled, WithLimiter(custom))

		assert.Same(t, custom, svc.limiter)
	})

	t.Run("explicit nil disables", func(t *testing.T) {
		svc := newLimiterService(t, enabled, WithLimiter(nil))

		assert.Nil(t, svc.limiter)
	})
}

// Shutdown must tolerate a service whose rate limiting is disabled.
func TestShutdown_WithoutLimiter(t *testing.T) {
	svc := NewService(nil, nil, &stubConfigProvider{}, &disabled.Provider{}, WithLimiter(nil))

	assert.NotPanics(t, svc.Shutdown)
}

// spyLimiter records whether Stop was called on it.
type spyLimiter struct{ stopped bool }

func (s *spyLimiter) Allow(string) error { return nil }
func (s *spyLimiter) Stop()              { s.stopped = true }

// Shutdown is not only a process-exit hook: ManagementServiceProvider.Update calls it on
// every public-parameters change. A limiter the application installed outlives that, because
// the application - not this service - owns its resources, and the managers this service
// already handed out keep calling Allow on it.
func TestShutdown_DoesNotStopAnApplicationSuppliedLimiter(t *testing.T) {
	spy := &spyLimiter{}
	svc := NewService(nil, nil, &stubConfigProvider{}, &disabled.Provider{}, WithLimiter(spy))

	svc.Shutdown()

	assert.False(t, spy.stopped, "a limiter passed with WithLimiter belongs to the application")
	require.NoError(t, spy.Allow("alice"), "the limiter is still usable after Shutdown")
}

// A limiter this service built from the configuration is its own to stop.
func TestShutdown_StopsALimiterItCreated(t *testing.T) {
	svc := NewService(nil, nil, &stubConfigProvider{apply: func(c *config.Config) {
		c.RateLimitEnabled = true
	}}, &disabled.Provider{})
	require.NotNil(t, svc.limiter)

	assert.True(t, svc.ownsLimiter)
	assert.NotPanics(t, svc.Shutdown)
}

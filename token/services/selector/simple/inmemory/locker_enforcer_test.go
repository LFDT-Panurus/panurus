/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRateLimiter is an application-supplied RateLimiter for tests.
type fakeRateLimiter struct {
	allow    error
	calls    int
	stopped  bool
	lastSeen string
}

func (f *fakeRateLimiter) Allow(identity string) error {
	f.calls++
	f.lastSeen = identity

	return f.allow
}

func (f *fakeRateLimiter) Stop() { f.stopped = true }

func TestNewEnforcer_CustomRateLimiterUsed(t *testing.T) {
	fake := &fakeRateLimiter{allow: errors.New("nope")}
	e := NewEnforcer(LockerConfig{RateLimiter: fake})

	// The custom limiter is consulted, and any error surfaces as ErrRateLimitExceeded.
	err := e.CheckRateLimit("alice")
	require.Error(t, err)
	assert.True(t, errors.Is(err, simple.ErrRateLimitExceeded), "got: %v", err)
	assert.Equal(t, 1, fake.calls)
	assert.Equal(t, "alice", fake.lastSeen)

	// When the custom limiter allows, CheckRateLimit returns nil.
	fake.allow = nil
	require.NoError(t, e.CheckRateLimit("alice"))
	assert.Equal(t, 2, fake.calls)
}

func TestNewEnforcer_CustomRateLimiterTakesPrecedenceOverConfig(t *testing.T) {
	fake := &fakeRateLimiter{}
	// RateLimit is set, but a custom limiter is also supplied: the custom one wins
	// and no built-in limiter is created.
	e := NewEnforcer(LockerConfig{RateLimit: 5, RateLimitBurst: 5, RateLimiter: fake})

	require.NoError(t, e.CheckRateLimit("bob"))
	assert.Equal(t, 1, fake.calls, "the supplied limiter should be the one consulted")
}

func TestEnforcer_Stop_DoesNotStopApplicationLimiter(t *testing.T) {
	fake := &fakeRateLimiter{}
	e := NewEnforcer(LockerConfig{RateLimiter: fake})

	e.Stop()
	assert.False(t, fake.stopped, "the enforcer must not stop an application-supplied limiter")
}

func TestEnforcer_Stop_StopsOwnedLimiter(t *testing.T) {
	// No custom limiter: the enforcer builds and owns a TokenBucketRateLimiter with a
	// background sweep goroutine (idleTTL > 0). Stop must shut it down without hanging.
	e := NewEnforcer(LockerConfig{RateLimit: 5, RateLimitBurst: 5, RateLimitIdleTTL: time.Minute})
	require.NotNil(t, e.rateLimiter)
	assert.True(t, e.ownsRateLimiter)

	e.Stop() // returns only after the owned limiter's sweep goroutine exits
}

func TestEnforcer_NoRateLimiterWhenUnconfigured(t *testing.T) {
	// Neither a custom limiter nor a positive RateLimit: no limiter, checks are no-ops.
	e := NewEnforcer(LockerConfig{})
	assert.Nil(t, e.rateLimiter)
	require.NoError(t, e.CheckRateLimit("alice"))
	e.Stop() // must not panic
}

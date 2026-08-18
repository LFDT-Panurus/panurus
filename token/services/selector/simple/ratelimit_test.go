/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"testing"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRateLimitedManager verifies that the built-in limiter, wrapped around a real simple manager,
// throttles per wallet without leaking locks: the denied selection never reaches the locker, so
// nothing is left locked and nothing has to be released.
func TestRateLimitedManager(t *testing.T) {
	locker := &recordingLocker{lockFailAfter: 100} // every lock succeeds
	qs := &mockQueryService{tokens: makeTokens(2, "USD", -1)}
	ctx := context.Background()

	mgr := NewManager(locker, func() QueryService { return qs }, 1, 0, false, precision)
	// A burst of one request, and a rate slow enough that nothing refills during the test.
	limited := ratelimit.Decorate(mgr, ratelimit.New(ratelimit.Config{Rate: 0.001, Burst: 1}), "n1,c1,ns1")

	selector, err := limited.NewSelector("testTx")
	require.NoError(t, err)

	// The first selection spends the wallet's single request and locks a token.
	ids, sum, err := selector.Select(ctx, &ownerFilter{id: "wallet1"}, "0x1", "USD")
	require.NoError(t, err)
	assert.Len(t, ids, 1)
	assert.NotNil(t, sum)
	locksSoFar := locker.calls
	assert.Positive(t, locksSoFar)

	// The second one is denied before any token is even looked at.
	ids, sum, err = selector.Select(ctx, &ownerFilter{id: "wallet1"}, "0x1", "USD")
	require.ErrorIs(t, err, token.SelectorRateLimited)
	assert.Nil(t, ids)
	assert.Nil(t, sum)
	assert.Equal(t, locksSoFar, locker.calls, "a denied selection must not lock anything")
	assert.Empty(t, locker.totalUnlocked(), "a denied selection has nothing to release")

	// Another wallet is unaffected.
	_, _, err = selector.Select(ctx, &ownerFilter{id: "wallet2"}, "0x1", "USD")
	require.NoError(t, err)

	// Releasing the tokens of a throttled wallet must always be possible.
	require.NoError(t, limited.Unlock(ctx, "testTx"))
	require.NoError(t, limited.Close("testTx"))
}

// TestLoaderRateLimiting verifies the service wiring: managers are handed out undecorated unless a
// limiter is configured or supplied.
func TestLoaderRateLimiting(t *testing.T) {
	tests := []struct {
		name      string
		opts      []ratelimit.Option
		decorated bool
	}{
		{name: "disabled by default", opts: nil, decorated: false},
		{name: "explicitly disabled", opts: []ratelimit.Option{ratelimit.WithLimiter(nil)}, decorated: false},
		{name: "default limiter", opts: []ratelimit.Option{ratelimit.WithDefaultLimiter()}, decorated: true},
		{
			name:      "custom limiter",
			opts:      []ratelimit.Option{ratelimit.WithLimiter(ratelimit.New(ratelimit.Config{Rate: 1, Burst: 1}))},
			decorated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locker := &recordingLocker{lockFailAfter: 100}
			qs := &mockQueryService{tokens: makeTokens(1, "USD", -1)}
			l := &loader{
				lockerProvider:       &fixedLockerProvider{locker: locker},
				numRetries:           1,
				requestCertification: false,
				limiter:              ratelimit.CompileOptions(tt.opts...).Limiter(&disabledRateLimitConfig{}),
			}

			mgr := l.newManager(locker, qs, precision, "n1,c1,ns1")
			_, plain := mgr.(*Manager)
			assert.Equal(t, tt.decorated, !plain, "manager decorated: %t", !plain)
		})
	}
}

// fixedLockerProvider hands out a fixed Locker.
type fixedLockerProvider struct {
	locker Locker
}

func (p *fixedLockerProvider) New(_, _, _ string) (Locker, error) {
	return p.locker, nil
}

// disabledRateLimitConfig is a ratelimit.Configuration with rate limiting off, standing in for the
// default token.selector configuration.
type disabledRateLimitConfig struct{}

func (c *disabledRateLimitConfig) IsRateLimitEnabled() bool    { return false }
func (c *disabledRateLimitConfig) GetRateLimit() float64       { return 0 }
func (c *disabledRateLimitConfig) GetRateLimitBurst() int      { return 0 }
func (c *disabledRateLimitConfig) GetRateLimitMaxBuckets() int { return 0 }

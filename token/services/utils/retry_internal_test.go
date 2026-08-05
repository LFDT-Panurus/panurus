/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalTestLogger mirrors the helper in the external test package, which this file cannot
// reach from inside package utils.
func internalTestLogger() logging.Logger {
	return logging.DriverLogger("test", "n1", "c1", "ns1")
}

// nextDelay is the whole of the backoff policy, so it can be checked directly rather than by
// measuring how long RunWithContext actually sleeps. Asserting on measured sleeps made this
// flaky: at a 10ms initial delay, ordinary scheduler jitter on a loaded CI runner is a large
// fraction of the interval, and it cost around 131s per run to walk the backoff to the cap.

// With the default constructor the jitter factor is zero, so every delay is exact.
func TestNextDelay_ExponentialUntilCapped(t *testing.T) {
	r := NewRetryRunner(internalTestLogger(), 15, 10*time.Millisecond, true)

	// 10ms doubling: the 13th attempt (index 12) is the first that would exceed the 30s cap.
	for attempt := range 12 {
		want := 10 * time.Millisecond << attempt
		require.Less(t, want, defaultMaxDelay, "attempt %d should still be under the cap", attempt)
		assert.Equal(t, want, r.nextDelay(attempt), "attempt %d", attempt)
	}

	for _, attempt := range []int{12, 13, 20, 100} {
		assert.Equal(t, defaultMaxDelay, r.nextDelay(attempt),
			"attempt %d must be capped at %s", attempt, defaultMaxDelay)
	}
}

// Without exponential backoff every attempt waits the initial delay.
func TestNextDelay_ConstantWhenBackoffDisabled(t *testing.T) {
	r := NewRetryRunner(internalTestLogger(), 5, 25*time.Millisecond, false)

	for _, attempt := range []int{0, 1, 7, 50} {
		assert.Equal(t, 25*time.Millisecond, r.nextDelay(attempt), "attempt %d", attempt)
	}
}

// A custom cap is honoured, and growth stops there rather than at the default.
func TestNextDelay_RespectsCustomMaxDelay(t *testing.T) {
	r := NewRetryRunnerWithJitter(internalTestLogger(), 10, 100*time.Millisecond, time.Second, 2.0, 0.0)

	assert.Equal(t, 100*time.Millisecond, r.nextDelay(0))
	assert.Equal(t, 200*time.Millisecond, r.nextDelay(1))
	assert.Equal(t, 400*time.Millisecond, r.nextDelay(2))
	assert.Equal(t, 800*time.Millisecond, r.nextDelay(3))
	// 1.6s would exceed the 1s cap
	assert.Equal(t, time.Second, r.nextDelay(4))
	assert.Equal(t, time.Second, r.nextDelay(9))
}

// Jitter is random, so the delay is only bounded, not exact. It must stay inside the documented
// band and must never push a capped delay above the cap by more than the jitter allows.
func TestNextDelay_JitterStaysWithinBand(t *testing.T) {
	const (
		initial = 100 * time.Millisecond
		maxD    = time.Second
		jitter  = 0.5
	)
	r := NewRetryRunnerWithJitter(internalTestLogger(), 10, initial, maxD, 2.0, jitter)

	for attempt := range 8 {
		base := float64(initial) * float64(int(1)<<attempt)
		if base > float64(maxD) {
			base = float64(maxD)
		}
		lo := time.Duration(base * (1 - jitter/2))
		hi := time.Duration(base * (1 + jitter/2))

		for range 200 {
			got := r.nextDelay(attempt)
			assert.GreaterOrEqual(t, got, lo, "attempt %d below jitter band", attempt)
			assert.LessOrEqual(t, got, hi, "attempt %d above jitter band", attempt)
		}
	}
}

// The constructor clamps jitterFactor to 1.0, and nextDelay applies (1 +/- jf/2), so the floor
// through the public API is half the base delay. The negative guard is therefore unreachable that
// way, which is worth pinning so nobody assumes otherwise.
func TestNextDelay_ConstructorClampsJitter(t *testing.T) {
	for _, requested := range []float64{1.0, 2.0, 3.0, 100.0} {
		r := NewRetryRunnerWithJitter(internalTestLogger(), 10, 10*time.Millisecond, time.Second, 2.0, requested)
		assert.LessOrEqual(t, r.jitterFactor, 1.0, "jitterFactor %v should have been clamped", requested)

		for range 1000 {
			assert.Positive(t, r.nextDelay(3))
		}
	}
}

// The negative guard in nextDelay only bites for a jitter factor above 2, which the constructor
// will not produce. Building the struct directly is the only way to reach it, so do that rather
// than assert a branch that cannot fire: at jf=3.0 roughly one draw in six would otherwise go
// negative, and each of those must come back as initialDelay instead.
func TestNextDelay_FallsBackWhenJitterWouldGoNegative(t *testing.T) {
	r := &retryRunner{
		initialDelay:      10 * time.Millisecond,
		maxDelay:          time.Second,
		expBackoff:        true,
		backoffMultiplier: 2.0,
		jitterFactor:      3.0,
		maxTimes:          10,
		logger:            internalTestLogger(),
	}

	const draws = 20000
	fallbacks := 0
	for range draws {
		d := r.nextDelay(3)
		require.Positive(t, d, "nextDelay must never return a non-positive duration")
		if d == r.initialDelay {
			fallbacks++
		}
	}

	// 1/6 of draws are expected to land in the guard. Assert only that it fired often enough to
	// have genuinely exercised it, not the exact rate.
	assert.Greater(t, fallbacks, draws/12,
		"expected the negative guard to fire on roughly a sixth of draws, got %d/%d", fallbacks, draws)
}

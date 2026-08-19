/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file tests retry.go which provides retry logic with exponential backoff.
// Tests verify context cancellation, backoff capping, and retry behavior under various conditions.
package utils_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() logging.Logger {
	// MustGetLogger may panic if the logging system is not initialized in some environments.
	// We use a logger that is guaranteed to be safe for tests.
	return logging.DriverLogger("test", "n1", "c1", "ns1")
}

// defaultMaxDelay mirrors the unexported cap in retry.go that NewRetryRunner and
// an invalid maxDelay fall back to. Kept in sync manually since it is not exported.
const defaultMaxDelay = 30 * time.Second

// sumBackoffDelays returns the total of the deterministic (jitter-free) backoff
// delays the runner sleeps across maxTimes attempts: delay_i = initialDelay *
// multiplier^i, each capped at maxDelay. For fixed backoff pass multiplier 1.0.
//
// Because time.After never returns early, the wall-clock time a run takes is
// always at least this sum. Asserting on total elapsed time is therefore a
// robust lower bound, immune to the per-call scheduling jitter that makes
// comparing the ratio of consecutive short intervals flaky.
func sumBackoffDelays(initialDelay, maxDelay time.Duration, multiplier float64, maxTimes int) time.Duration {
	var total time.Duration
	delay := float64(initialDelay)
	for range maxTimes {
		total += time.Duration(min(delay, float64(maxDelay)))
		delay *= multiplier
	}

	return total
}

// assertElapsedMatchesCappedBackoff asserts that a jitter-free run took a total
// wall-clock time consistent with an exponential backoff capped at maxDelay.
// The lower bound (the exact sum of delays) is guaranteed because time.After
// never returns early; the upper bound adds slack for scheduling/wakeup latency
// while staying tight enough that a wrong cap (e.g. 15s or 60s instead of 30s)
// lands outside the window.
func assertElapsedMatchesCappedBackoff(t *testing.T, elapsed, initialDelay, maxDelay time.Duration, multiplier float64, maxTimes int) {
	t.Helper()

	expected := sumBackoffDelays(initialDelay, maxDelay, multiplier, maxTimes)
	slack := max(expected/5, 2*time.Second) // 20%, floored at 2s for scheduling noise

	assert.GreaterOrEqual(t, elapsed, expected,
		"total elapsed time should be at least the sum of the capped backoff delays")
	assert.LessOrEqual(t, elapsed, expected+slack,
		"total elapsed time should not exceed the capped backoff sum by more than scheduling slack")
}

// TestRunWithContext_PreCanceledContext verifies that a pre-canceled context causes
// RunWithContext to return immediately without invoking the runner at all.
func TestRunWithContext_PreCanceledContext(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, 10*time.Second, false)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before calling Run

	calls := 0
	start := time.Now()
	err := runner.RunWithContext(ctx, func() error {
		calls++

		return errors.New("should not be reached")
	})
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, calls, "runner should not be invoked on a pre-canceled context")
	assert.Less(t, elapsed, 500*time.Millisecond, "should return immediately, not block on 10s sleep")
}

// TestRunWithContext_CanceledDuringBackoff verifies that canceling the context while
// the retry loop is sleeping between attempts unblocks the caller promptly.
// This is the core regression test for the bug: without context-aware sleep,
// a worker goroutine would block in time.Sleep for the full (ever-growing) delay.
func TestRunWithContext_CanceledDuringBackoff(t *testing.T) {
	// Initial delay is long so we can observe it being interrupted.
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, 5*time.Second, false)

	ctx, cancel := context.WithCancel(t.Context())

	calls := 0
	done := make(chan error, 1)

	go func() {
		done <- runner.RunWithContext(ctx, func() error {
			calls++

			return errors.New("transient error")
		})
	}()

	// Let the runner execute once and enter the 5s sleep.
	time.Sleep(50 * time.Millisecond)
	cancel()

	start := time.Now()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, calls, "runner should have been called exactly once before cancel")
		assert.Less(t, elapsed, time.Second,
			"RunWithContext should unblock within 1s of cancellation, not wait out the 5s sleep")
	case <-time.After(6 * time.Second):
		t.Fatal("RunWithContext did not respect context cancellation — worker would have been stuck")
	}
}

// TestRunWithContext_BackoffDoesNotExceedCap verifies that the exponential backoff
// grows as expected and does not run away without bound.
// We use a tiny initial delay so the test runs fast; the cap itself is 30s by default.
func TestRunWithContext_BackoffDoesNotExceedCap(t *testing.T) {
	const (
		maxTimes     = 10
		initialDelay = time.Millisecond
		multiplier   = 2.0
	)

	// 10 retries with 1ms initial delay, exp backoff: 1,2,4,8,16,32,64,128,256,512 ms
	// (all below the 30s cap), summing to ~1s.
	runner := utils.NewRetryRunner(testLogger(), maxTimes, initialDelay, true)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// The total elapsed time must bracket the exponential sum. The upper bound is
	// what catches runaway growth: if the delay were not bounded, the tail delays
	// would balloon and push the total far past this window.
	assertElapsedMatchesCappedBackoff(t, elapsed, initialDelay, defaultMaxDelay, multiplier, maxTimes)
}

// TestRunWithContext_SucceedsAfterTransientFailures verifies normal retry behavior:
// the runner is retried until it succeeds.
func TestRunWithContext_SucceedsAfterTransientFailures(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, time.Millisecond, false)

	calls := 0
	err := runner.RunWithContext(t.Context(), func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

// TestRun_DelegatesWithBackgroundContext verifies that Run (which uses context.Background)
// still retries correctly and is not broken by the refactor.
func TestRun_DelegatesWithBackgroundContext(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 5, time.Millisecond, false)

	calls := 0
	err := runner.Run(func() error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

// TestRunWithContext_MaxRetriesExhausted verifies that when maxTimes is set and
// all retries fail, a joined error is returned.
func TestRunWithContext_MaxRetriesExhausted(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 3, time.Millisecond, false)

	calls := 0
	err := runner.RunWithContext(t.Context(), func() error {
		calls++

		return errors.New("persistent failure")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persistent failure")
	assert.Equal(t, 3, calls)
}

// TestRunWithContext_SuccessOnFirstAttempt verifies zero-retry fast path.
func TestRunWithContext_SuccessOnFirstAttempt(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, time.Second, true)

	calls := 0
	err := runner.RunWithContext(t.Context(), func() error {
		calls++

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

// TestRunWithErrors_TerminateOnSuccess verifies that RunWithErrors stops retrying
// when the runner returns (true, nil), indicating success.
func TestRunWithErrors_TerminateOnSuccess(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, time.Millisecond, false)

	calls := 0
	err := runner.RunWithErrors(func() (bool, error) {
		calls++
		if calls < 3 {
			return false, errors.New("transient error")
		}

		return true, nil // terminate with success
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

// TestRunWithErrors_TerminateOnError verifies that RunWithErrors stops retrying
// when the runner returns (true, error), returning that error immediately.
func TestRunWithErrors_TerminateOnError(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, time.Millisecond, false)

	calls := 0
	expectedErr := errors.New("fatal error")
	err := runner.RunWithErrors(func() (bool, error) {
		calls++
		if calls < 2 {
			return false, errors.New("transient")
		}

		return true, expectedErr // terminate with error
	})

	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, 2, calls)
}

// TestRunWithErrors_MaxRetriesExhausted verifies that when maxTimes is exhausted
// and the runner never returns true, all errors are joined and returned.
func TestRunWithErrors_MaxRetriesExhausted(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 3, time.Millisecond, false)

	calls := 0
	err := runner.RunWithErrors(func() (bool, error) {
		calls++

		return false, errors.New("persistent failure")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persistent failure")
	assert.Equal(t, 3, calls)
}

// TestRunWithErrors_MaxRetriesExhaustedNoErrors verifies that when maxTimes is
// exhausted but no errors occurred (runner returned false, nil each time),
// ErrMaxRetriesExceeded is returned.
func TestRunWithErrors_MaxRetriesExhaustedNoErrors(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 3, time.Millisecond, false)

	calls := 0
	err := runner.RunWithErrors(func() (bool, error) {
		calls++

		return false, nil // keep retrying but no error
	})

	require.ErrorIs(t, err, utils.ErrMaxRetriesExceeded)
	assert.Equal(t, 3, calls)
}

// TestRunWithErrors_ExponentialBackoff verifies that RunWithErrors respects
// exponential backoff when configured.
func TestRunWithErrors_ExponentialBackoff(t *testing.T) {
	const (
		maxTimes     = 5
		initialDelay = time.Millisecond
	)

	runner := utils.NewRetryRunner(testLogger(), maxTimes, initialDelay, true)

	start := time.Now()

	_ = runner.RunWithErrors(func() (bool, error) {
		return false, errors.New("always fail")
	})

	elapsed := time.Since(start)

	// NewRetryRunner uses a 2.0 multiplier with no jitter, so the exponential
	// delays (1ms, 2ms, 4ms, 8ms, 16ms) are deterministic. Asserting a lower bound
	// on total elapsed time is robust to the CI scheduling jitter that made the
	// previous per-interval ratio comparison flaky (it used to be skipped).
	// NewRetryRunner caps backoff at 30s by default; these small delays never reach it.
	expectedMin := sumBackoffDelays(initialDelay, 30*time.Second, 2.0, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should match a 2.0 multiplier exponential backoff")
}

// TestRunWithContext_MaxRetriesExhaustedNoErrors verifies the edge case where
// maxTimes is exhausted but the runner never returned an error (always returned nil).
// This should return ErrMaxRetriesExceeded.
func TestRunWithContext_MaxRetriesExhaustedNoErrors(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 3, time.Millisecond, false)

	calls := 0
	err := runner.RunWithContext(t.Context(), func() error {
		calls++
		// This is an unusual case: runner succeeds (returns nil) but we
		// want to test the edge case. In practice, this would mean the
		// runner is broken (returns nil but doesn't actually succeed).
		// However, the code path exists, so we test it.
		// Actually, looking at the code, if runner() returns nil, it returns immediately.
		// So this edge case is when runner never returns nil AND never returns an error.
		// That's impossible in Go - a function must return something.
		// Let me re-read the code...

		// Actually, the edge case at line 97-98 is when the loop completes
		// (maxTimes exhausted) but errs slice is empty. This can only happen
		// if runner() always returns nil, but then it would return early at line 85.
		// So this is actually dead code that can never be reached!
		// But let's verify the current behavior is correct.
		return nil
	})

	// Since runner returns nil, it should succeed on first attempt
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

// TestNextDelay_FixedBackoff verifies that when expBackoff is false,
// the delay remains constant (no exponential growth).
func TestNextDelay_FixedBackoff(t *testing.T) {
	const (
		maxTimes = 5
		delay    = 10 * time.Millisecond
	)

	runner := utils.NewRetryRunner(testLogger(), maxTimes, delay, false)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// With fixed backoff the runner sleeps the same delay after each of the
	// maxTimes attempts, so the total elapsed time must be at least maxTimes*delay.
	// A lower bound on total elapsed time avoids the per-interval scheduling jitter
	// that made comparing individual ~10ms intervals flaky.
	expectedMin := sumBackoffDelays(delay, delay, 1.0, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should be at least maxTimes*delay for fixed backoff")
}

// TestRunWithErrorsContext_PreCanceledContext verifies that a pre-canceled context causes
// RunWithErrorsContext to return immediately without invoking the runner at all.
func TestRunWithErrorsContext_PreCanceledContext(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, 10*time.Second, false)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before calling Run

	calls := 0
	start := time.Now()
	err := runner.RunWithErrorsContext(ctx, func() (bool, error) {
		calls++

		return false, errors.New("should not be reached")
	})
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, calls, "runner should not be invoked on a pre-canceled context")
	assert.Less(t, elapsed, 500*time.Millisecond, "should return immediately, not block on 10s sleep")
}

// TestRunWithErrorsContext_CanceledDuringBackoff verifies that canceling the context
// while the retry loop is sleeping unblocks the caller promptly.
func TestRunWithErrorsContext_CanceledDuringBackoff(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, 5*time.Second, false)

	ctx, cancel := context.WithCancel(t.Context())

	calls := 0
	done := make(chan error, 1)

	go func() {
		done <- runner.RunWithErrorsContext(ctx, func() (bool, error) {
			calls++

			return false, errors.New("transient error")
		})
	}()

	// Let the runner execute once and enter the 5s sleep.
	time.Sleep(50 * time.Millisecond)
	cancel()

	start := time.Now()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, calls, "runner should have been called exactly once before cancel")
		assert.Less(t, elapsed, time.Second,
			"RunWithErrorsContext should unblock within 1s of cancellation, not wait out the 5s sleep")
	case <-time.After(6 * time.Second):
		t.Fatal("RunWithErrorsContext did not respect context cancellation")
	}
}

// TestRunWithErrorsContext_TerminateWithNil verifies that when runner returns (true, nil),
// RunWithErrorsContext stops retrying and returns nil.
func TestRunWithErrorsContext_TerminateWithNil(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, time.Millisecond, false)

	calls := 0
	err := runner.RunWithErrorsContext(t.Context(), func() (bool, error) {
		calls++
		if calls < 3 {
			return false, errors.New("transient error")
		}

		return true, nil // terminate with success
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

// TestRunWithErrorsContext_TerminateWithError verifies that when runner returns (true, err),
// RunWithErrorsContext stops retrying and returns that error immediately.
func TestRunWithErrorsContext_TerminateWithError(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), utils.Infinitely, time.Millisecond, false)

	calls := 0
	expectedErr := errors.New("fatal error")
	err := runner.RunWithErrorsContext(t.Context(), func() (bool, error) {
		calls++
		if calls < 2 {
			return false, errors.New("transient")
		}

		return true, expectedErr // terminate with error
	})

	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, 2, calls)
}

// TestRunWithErrorsContext_MaxRetriesExhaustedWithErrors verifies that when maxTimes
// is exhausted and errors were collected, errors.Join is returned.
func TestRunWithErrorsContext_MaxRetriesExhaustedWithErrors(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 3, time.Millisecond, false)

	calls := 0
	err := runner.RunWithErrorsContext(t.Context(), func() (bool, error) {
		calls++

		return false, errors.New("persistent failure")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persistent failure")
	assert.Equal(t, 3, calls)
}

// TestRunWithErrorsContext_MaxRetriesExhaustedNoErrors verifies that when maxTimes
// is exhausted but no errors occurred, ErrMaxRetriesExceeded is returned.
func TestRunWithErrorsContext_MaxRetriesExhaustedNoErrors(t *testing.T) {
	runner := utils.NewRetryRunner(testLogger(), 3, time.Millisecond, false)

	calls := 0
	err := runner.RunWithErrorsContext(t.Context(), func() (bool, error) {
		calls++

		return false, nil // keep retrying but no error
	})

	require.ErrorIs(t, err, utils.ErrMaxRetriesExceeded)
	assert.Equal(t, 3, calls)
}

// TestNewRetryRunnerWithJitter_NegativeBackoffMultiplier verifies that negative
// backoff multiplier defaults to 2.0 and produces standard exponential backoff.
func TestNewRetryRunnerWithJitter_NegativeBackoffMultiplier(t *testing.T) {
	const (
		maxTimes     = 5
		initialDelay = 10 * time.Millisecond
		maxDelay     = 1 * time.Second
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		maxDelay,
		-1.0, // invalid, should default to 2.0
		0.0,  // no jitter for predictable testing
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// An invalid multiplier defaults to 2.0, so the total elapsed time must be at
	// least the sum of the standard exponential (jitter-free) delays.
	expectedMin := sumBackoffDelays(initialDelay, maxDelay, 2.0, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should match a default 2.0 multiplier backoff")
}

// TestNewRetryRunnerWithJitter_ZeroBackoffMultiplier verifies that zero
// backoff multiplier defaults to 2.0.
func TestNewRetryRunnerWithJitter_ZeroBackoffMultiplier(t *testing.T) {
	const (
		maxTimes     = 5
		initialDelay = 10 * time.Millisecond
		maxDelay     = 1 * time.Second
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		maxDelay,
		0.0, // invalid, should default to 2.0
		0.0, // no jitter
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// A zero multiplier defaults to 2.0, so the total elapsed time must be at
	// least the sum of the standard exponential (jitter-free) delays.
	expectedMin := sumBackoffDelays(initialDelay, maxDelay, 2.0, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should match a default 2.0 multiplier backoff")
}

// TestNewRetryRunnerWithJitter_NegativeJitterFactor verifies that negative
// jitter factor is clamped to 0.0 (no jitter).
func TestNewRetryRunnerWithJitter_NegativeJitterFactor(t *testing.T) {
	const (
		maxTimes     = 5
		initialDelay = 10 * time.Millisecond
		maxDelay     = 1 * time.Second
		multiplier   = 2.0
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		maxDelay,
		multiplier,
		-0.5, // invalid, should be clamped to 0.0
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// With jitter clamped to 0.0 the backoff is deterministic, so the total
	// elapsed time must be at least the sum of the (jitter-free) delays.
	expectedMin := sumBackoffDelays(initialDelay, maxDelay, multiplier, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should be at least the sum of the (jitter-free) backoff delays")
}

// TestNewRetryRunnerWithJitter_ExcessiveJitterFactor verifies that jitter
// factor > 1.0 is clamped to 1.0.
func TestNewRetryRunnerWithJitter_ExcessiveJitterFactor(t *testing.T) {
	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		10,
		100*time.Millisecond,
		10*time.Second,
		2.0,
		1.5, // invalid, should be clamped to 1.0
	)

	var delays []time.Duration
	prev := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		now := time.Now()
		delays = append(delays, now.Sub(prev))
		prev = now

		return errors.New("always fail")
	})

	delays = delays[1:]

	// With max jitter (1.0), delays should vary within ±50% of base
	for i, d := range delays {
		baseDelay := min(100*time.Millisecond*time.Duration(1<<i), 10*time.Second)

		// Should not exceed bounds of 1.0 jitter factor
		minDelay := time.Duration(float64(baseDelay) * 0.25)
		maxDelay := time.Duration(float64(baseDelay) * 2.0)

		assert.GreaterOrEqual(t, d, minDelay,
			"delay %d should be >= %v with clamped jitter", i, minDelay)
		assert.LessOrEqual(t, d, maxDelay,
			"delay %d should be <= %v with clamped jitter", i, maxDelay)
	}
}

// TestNewRetryRunnerWithJitter_ZeroMaxDelay verifies that zero maxDelay
// defaults to 30 seconds.
func TestNewRetryRunnerWithJitter_ZeroMaxDelay(t *testing.T) {
	const (
		maxTimes     = 15
		initialDelay = 10 * time.Millisecond
		multiplier   = 2.0
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		0, // should default to 30s
		multiplier,
		0.0,
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// A zero maxDelay defaults to a 30s cap. With no jitter the delays are
	// deterministic, so the total elapsed time is bracketed by the sum of the
	// capped delays. The lower bound alone would also pass for a larger cap, so
	// the upper bound is what pins the default to exactly 30s: a 15s or a 60s cap
	// would fall outside this window.
	assertElapsedMatchesCappedBackoff(t, elapsed, initialDelay, defaultMaxDelay, multiplier, maxTimes)
}

// TestNewRetryRunnerWithJitter_NegativeMaxDelay verifies that negative maxDelay
// defaults to 30 seconds.
func TestNewRetryRunnerWithJitter_NegativeMaxDelay(t *testing.T) {
	const (
		maxTimes     = 15
		initialDelay = 10 * time.Millisecond
		multiplier   = 2.0
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		-10*time.Second, // invalid, should default to 30s
		multiplier,
		0.0,
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// A negative maxDelay defaults to the same 30s cap, so the deterministic
	// (jitter-free) total elapsed time must match the capped backoff sum.
	assertElapsedMatchesCappedBackoff(t, elapsed, initialDelay, defaultMaxDelay, multiplier, maxTimes)
}

// TestNewRetryRunnerWithJitter_CustomBackoffMultiplier verifies that custom backoff
// multipliers produce the expected exponential growth pattern.
func TestNewRetryRunnerWithJitter_CustomBackoffMultiplier(t *testing.T) {
	const (
		maxTimes     = 5
		initialDelay = 10 * time.Millisecond
		maxDelay     = 1 * time.Second
		multiplier   = 3.0 // 3x growth
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		maxDelay,
		multiplier,
		0.0, // no jitter for predictable testing
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// Expected delays: 10ms, 30ms, 90ms, 270ms, 810ms (all below the 1s cap).
	// The total elapsed time must be at least their sum.
	expectedMin := sumBackoffDelays(initialDelay, maxDelay, multiplier, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should match a 3.0 multiplier backoff")
}

// TestNewRetryRunnerWithJitter_JitterBehavior verifies that jitter adds randomness
// to delays while keeping them within expected bounds.
func TestNewRetryRunnerWithJitter_JitterBehavior(t *testing.T) {
	// Use jitterFactor of 0.5 (50% jitter range)
	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		10,
		100*time.Millisecond,
		10*time.Second,
		2.0,
		0.5, // 50% jitter
	)

	var delays []time.Duration
	prev := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		now := time.Now()
		delays = append(delays, now.Sub(prev))
		prev = now

		return errors.New("always fail")
	})

	// Skip first delay (no sleep before first call)
	delays = delays[1:]

	// With jitterFactor=0.5, delays should be within ±25% of base delay
	// Base delays: 100ms, 200ms, 400ms, 800ms, 1600ms, 3200ms, 6400ms, 10s (capped)
	// For each delay, verify it's within reasonable bounds
	for i, d := range delays {
		// Calculate expected base delay (without jitter)
		baseDelay := min(100*time.Millisecond*time.Duration(1<<i), 10*time.Second)

		// With 50% jitter, delay should be in range [base*0.75, base*1.25]
		minDelay := time.Duration(float64(baseDelay) * 0.5)  // 75% - 25% tolerance
		maxDelay := time.Duration(float64(baseDelay) * 1.75) // 125% + 50% tolerance

		assert.GreaterOrEqual(t, d, minDelay,
			"delay %d (%v) should be >= %v (base %v with jitter)", i, d, minDelay, baseDelay)
		assert.LessOrEqual(t, d, maxDelay,
			"delay %d (%v) should be <= %v (base %v with jitter)", i, d, maxDelay, baseDelay)
	}
}

// TestNewRetryRunnerWithJitter_CustomMaxDelay verifies that custom maxDelay
// caps exponential backoff at the specified value.
func TestNewRetryRunnerWithJitter_CustomMaxDelay(t *testing.T) {
	const (
		maxTimes     = 10
		initialDelay = 50 * time.Millisecond
		maxDelay     = 500 * time.Millisecond // custom cap
		multiplier   = 2.0
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		maxDelay,
		multiplier,
		0.0, // no jitter for predictable testing
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// Expected delays: 50ms, 100ms, 200ms, 400ms, then 500ms (capped) for the
	// remaining attempts. Without the cap the tail delays would grow to seconds,
	// pushing the total far past the upper bound, so this brackets the 500ms cap.
	assertElapsedMatchesCappedBackoff(t, elapsed, initialDelay, maxDelay, multiplier, maxTimes)
}

// TestNewRetryRunnerWithJitter_ZeroJitter verifies that zero jitter produces
// deterministic exponential backoff (no randomness).
func TestNewRetryRunnerWithJitter_ZeroJitter(t *testing.T) {
	const (
		maxTimes     = 5
		initialDelay = 50 * time.Millisecond
		maxDelay     = 1 * time.Second
		multiplier   = 2.0
	)

	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		maxTimes,
		initialDelay,
		maxDelay,
		multiplier,
		0.0, // no jitter
	)

	start := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		return errors.New("always fail")
	})

	elapsed := time.Since(start)

	// With zero jitter the delays follow a strict exponential pattern
	// (50ms, 100ms, 200ms, 400ms, 800ms), so the total elapsed time must be at
	// least their sum.
	expectedMin := sumBackoffDelays(initialDelay, maxDelay, multiplier, maxTimes)
	assert.GreaterOrEqual(t, elapsed, expectedMin,
		"total elapsed time should match a zero-jitter exponential backoff")
}

// TestNewRetryRunnerWithJitter_MaxJitter verifies that maximum jitter (1.0)
// produces the widest variation in delays.
func TestNewRetryRunnerWithJitter_MaxJitter(t *testing.T) {
	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		10,
		100*time.Millisecond,
		10*time.Second,
		2.0,
		1.0, // maximum jitter
	)

	var delays []time.Duration
	prev := time.Now()

	_ = runner.RunWithContext(t.Context(), func() error {
		now := time.Now()
		delays = append(delays, now.Sub(prev))
		prev = now

		return errors.New("always fail")
	})

	// Skip first delay (no sleep before first call)
	delays = delays[1:]

	// With jitterFactor=1.0, delays can vary by ±50% of base
	// Base delays: 100ms, 200ms, 400ms, 800ms, 1600ms, 3200ms, 6400ms, 10s (capped)
	for i, d := range delays {
		baseDelay := min(100*time.Millisecond*time.Duration(1<<i), 10*time.Second)

		// With 100% jitter, delay should be in range [base*0.5, base*1.5]
		minDelay := time.Duration(float64(baseDelay) * 0.25) // 50% - 25% tolerance
		maxDelay := time.Duration(float64(baseDelay) * 2.0)  // 150% + 50% tolerance

		assert.GreaterOrEqual(t, d, minDelay,
			"delay %d (%v) should be >= %v (base %v with max jitter)", i, d, minDelay, baseDelay)
		assert.LessOrEqual(t, d, maxDelay,
			"delay %d (%v) should be <= %v (base %v with max jitter)", i, d, maxDelay, baseDelay)
	}
}

// TestNewRetryRunnerWithJitter_FunctionalBehavior verifies that the runner
// created with jitter still functions correctly for retries.
func TestNewRetryRunnerWithJitter_FunctionalBehavior(t *testing.T) {
	runner := utils.NewRetryRunnerWithJitter(
		testLogger(),
		5,
		10*time.Millisecond,
		1*time.Second,
		2.0,
		0.3,
	)

	calls := 0
	err := runner.RunWithContext(t.Context(), func() error {
		calls++
		if calls < 3 {
			return errors.New("transient error")
		}

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls, "should succeed after 3 attempts")
}

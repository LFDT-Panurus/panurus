/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_Allow(t *testing.T) {
	t.Run("allows requests within rate limit", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0) // 10 req/sec, burst of 10
		identity := "wallet1"

		// Should allow up to burst size immediately
		for i := range 10 {
			err := rl.Allow(identity)
			require.NoError(t, err, "request %d should be allowed", i)
		}

		// Next request should be rate limited
		err := rl.Allow(identity)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)
	})

	t.Run("refills tokens over time", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0) // 10 req/sec
		identity := "wallet1"

		// Exhaust the bucket
		for range 10 {
			_ = rl.Allow(identity)
		}

		// Should be rate limited
		err := rl.Allow(identity)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)

		// Wait for refill (100ms = 1 token at 10 req/sec)
		time.Sleep(150 * time.Millisecond)

		// Should allow one more request
		err = rl.Allow(identity)
		assert.NoError(t, err)
	})

	t.Run("isolates different identities", func(t *testing.T) {
		rl := NewRateLimiter(5, 5, 0, 0)
		identity1 := "wallet1"
		identity2 := "wallet2"

		// Exhaust identity1's quota
		for range 5 {
			err := rl.Allow(identity1)
			require.NoError(t, err)
		}

		// identity1 should be rate limited
		err := rl.Allow(identity1)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)

		// identity2 should still have full quota
		for i := range 5 {
			err := rl.Allow(identity2)
			require.NoError(t, err, "identity2 request %d should be allowed", i)
		}
	})

	t.Run("rejects empty identity", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0)
		err := rl.Allow("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "identity cannot be empty")
	})

	t.Run("handles concurrent requests", func(t *testing.T) {
		rl := NewRateLimiter(100, 100, 0, 0)
		identity := "wallet1"
		numGoroutines := 10
		requestsPerGoroutine := 15

		var wg sync.WaitGroup
		var mu sync.Mutex
		allowed := 0
		denied := 0

		for range numGoroutines {
			wg.Go(func() {
				for range requestsPerGoroutine {
					err := rl.Allow(identity)
					mu.Lock()
					if err == nil {
						allowed++
					} else {
						denied++
					}
					mu.Unlock()
				}
			})
		}

		wg.Wait()

		// Should allow exactly burst size (100)
		assert.Equal(t, 100, allowed, "should allow exactly burst size")
		assert.Equal(t, 50, denied, "remaining requests should be denied")
	})
}

func TestRateLimiter_Reset(t *testing.T) {
	t.Run("resets specific identity", func(t *testing.T) {
		rl := NewRateLimiter(5, 5, 0, 0)
		identity := "wallet1"

		// Exhaust quota
		for range 5 {
			_ = rl.Allow(identity)
		}

		// Should be rate limited
		err := rl.Allow(identity)
		require.ErrorIs(t, err, simple.ErrRateLimitExceeded)

		// Reset
		rl.Reset(identity)

		// Should have full quota again
		for i := range 5 {
			err := rl.Allow(identity)
			require.NoError(t, err, "request %d should be allowed after reset", i)
		}
	})

	t.Run("resets all identities", func(t *testing.T) {
		rl := NewRateLimiter(5, 5, 0, 0)
		identity1 := "wallet1"
		identity2 := "wallet2"

		// Exhaust both quotas
		for range 5 {
			_ = rl.Allow(identity1)
			_ = rl.Allow(identity2)
		}

		// Both should be rate limited
		require.ErrorIs(t, rl.Allow(identity1), simple.ErrRateLimitExceeded)
		require.ErrorIs(t, rl.Allow(identity2), simple.ErrRateLimitExceeded)

		// Reset all
		rl.ResetAll()

		// Both should have full quota
		assert.NoError(t, rl.Allow(identity1))
		assert.NoError(t, rl.Allow(identity2))
	})
}

func TestRateLimiter_GetStats(t *testing.T) {
	t.Run("returns stats for existing identity", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0)
		identity := "wallet1"

		// Make some requests
		for range 3 {
			_ = rl.Allow(identity)
		}

		tokens, exists := rl.GetStats(identity)
		assert.True(t, exists)
		assert.InDelta(t, 7.0, tokens, 0.1, "should have ~7 tokens remaining")
	})

	t.Run("returns false for non-existent identity", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0)
		_, exists := rl.GetStats("nonexistent")
		assert.False(t, exists)
	})

	t.Run("shows refill over time", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0)
		identity := "wallet1"

		// Exhaust bucket
		for range 10 {
			_ = rl.Allow(identity)
		}

		tokens, _ := rl.GetStats(identity)
		assert.InDelta(t, 0.0, tokens, 0.1)

		// Wait for refill
		time.Sleep(500 * time.Millisecond)

		tokens, _ = rl.GetStats(identity)
		assert.Greater(t, tokens, 4.0, "should have refilled ~5 tokens")
	})
}

func TestTokenBucket_Allow(t *testing.T) {
	t.Run("allows burst up to max tokens", func(t *testing.T) {
		tb := newTokenBucket(5, 1) // 5 tokens, 1 per second

		// Should allow 5 requests immediately
		for i := range 5 {
			assert.True(t, tb.allow(), "request %d should be allowed", i)
		}

		// 6th request should fail
		assert.False(t, tb.allow())
	})

	t.Run("refills at specified rate", func(t *testing.T) {
		tb := newTokenBucket(10, 10) // 10 tokens per second

		// Exhaust bucket
		for range 10 {
			tb.allow()
		}

		assert.False(t, tb.allow())

		// Wait for 1 token to refill (100ms at 10/sec)
		time.Sleep(150 * time.Millisecond)

		assert.True(t, tb.allow(), "should allow after refill")
	})

	t.Run("caps tokens at max", func(t *testing.T) {
		tb := newTokenBucket(5, 10)

		// Wait for potential overflow
		time.Sleep(1 * time.Second)

		// Should still only allow max tokens
		allowed := 0
		for range 10 {
			if tb.allow() {
				allowed++
			}
		}

		assert.Equal(t, 5, allowed, "should cap at max tokens")
	})
}

func TestRateLimiter_Parallel(t *testing.T) {
	// TestRateLimiter_Parallel verifies correct behaviour under high concurrency.

	t.Run("single identity: allowed count equals burst size", func(t *testing.T) {
		// With burst=50 and many more goroutines than burst, exactly 50 requests
		// must be allowed and the rest denied — no more, no less.
		const burst = 50
		rl := NewRateLimiter(float64(burst), float64(burst), 0, 0)
		defer rl.Stop()

		const goroutines = 20
		const requestsEach = 10 // total 200 attempts > 50 burst

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			allowed int
		)
		for range goroutines {
			wg.Go(func() {
				for range requestsEach {
					if rl.Allow("shared-identity") == nil {
						mu.Lock()
						allowed++
						mu.Unlock()
					}
				}
			})
		}
		wg.Wait()

		assert.Equal(t, burst, allowed, "exactly burst requests should be allowed")
	})

	t.Run("multiple identities: each gets independent burst", func(t *testing.T) {
		// N identities, each hammered by its own goroutine.
		// Every identity must allow exactly its burst and deny the rest.
		const (
			burst      = 5
			identities = 20
			attempts   = 15 // > burst per identity
		)
		rl := NewRateLimiter(float64(burst), float64(burst), 0, 0)
		defer rl.Stop()

		type result struct{ allowed, denied int }
		results := make([]result, identities)

		var wg sync.WaitGroup
		for i := range identities {
			wg.Go(func() {
				id := fmt.Sprintf("wallet-%d", i)
				for range attempts {
					if rl.Allow(id) == nil {
						results[i].allowed++
					} else {
						results[i].denied++
					}
				}
			})
		}
		wg.Wait()

		for i, r := range results {
			assert.Equal(t, burst, r.allowed, "identity %d: allowed should equal burst", i)
			assert.Equal(t, attempts-burst, r.denied, "identity %d: denied should equal attempts-burst", i)
		}
	})

	t.Run("concurrent bucket creation races on new identities", func(t *testing.T) {
		// Many goroutines all call Allow() on the SAME previously-unseen identity
		// at exactly the same moment, exercising the double-checked-locking path.
		// The result must still honour the burst cap — no panics, no races.
		const burst = 10
		rl := NewRateLimiter(float64(burst), float64(burst), 0, 0)
		defer rl.Stop()

		const goroutines = 50
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			allowed int
		)
		// Use a barrier so all goroutines fire simultaneously.
		start := make(chan struct{})
		for range goroutines {
			wg.Go(func() {
				<-start
				if rl.Allow("brand-new") == nil {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			})
		}
		close(start)
		wg.Wait()

		assert.LessOrEqual(t, allowed, burst, "must not exceed burst even under race")
		assert.Positive(t, allowed, "at least one request must be allowed")
	})

	t.Run("parallel eviction and Allow do not race", func(t *testing.T) {
		// Run Allow() and the idle-eviction sweep simultaneously to ensure
		// there are no data races (best detected with -race).
		ttl := 50 * time.Millisecond
		rl := NewRateLimiter(1000, 1000, ttl, 10*time.Millisecond)
		defer rl.Stop()

		var wg sync.WaitGroup
		stop := make(chan struct{})

		// Writer goroutines: continuously create and use buckets.
		for i := range 10 {
			wg.Go(func() {
				id := fmt.Sprintf("evict-wallet-%d", i)
				for {
					select {
					case <-stop:
						return
					default:
						_ = rl.Allow(id)
					}
				}
			})
		}

		// Let it run long enough for several eviction sweeps.
		time.Sleep(200 * time.Millisecond)
		close(stop)
		wg.Wait()
	})
}

func TestNewRateLimiter_BurstValidation(t *testing.T) {
	t.Run("adjusts burst to match rate if too small", func(t *testing.T) {
		rl := NewRateLimiter(10, 5, 0, 0) // burst < rate
		assert.InDelta(t, 10.0, rl.maxTokens, 0.01, "burst should be adjusted to match rate")
	})

	t.Run("keeps burst if larger than rate", func(t *testing.T) {
		rl := NewRateLimiter(10, 20, 0, 0)
		assert.InDelta(t, 20.0, rl.maxTokens, 0.01, "burst should remain as specified")
	})
}

func TestRateLimiter_IdleEviction(t *testing.T) {
	t.Run("evicts idle bucket after TTL", func(t *testing.T) {
		ttl := 100 * time.Millisecond
		rl := NewRateLimiter(10, 10, ttl, 20*time.Millisecond)
		defer rl.Stop()

		identity := "wallet1"
		require.NoError(t, rl.Allow(identity))

		// Bucket must exist immediately after use
		_, exists := rl.GetStats(identity)
		assert.True(t, exists)

		// Wait longer than TTL + one cleanup interval
		time.Sleep(200 * time.Millisecond)

		// Bucket should have been evicted
		_, exists = rl.GetStats(identity)
		assert.False(t, exists, "idle bucket should be evicted after TTL")
	})

	t.Run("keeps active bucket alive", func(t *testing.T) {
		ttl := 100 * time.Millisecond
		rl := NewRateLimiter(100, 100, ttl, 20*time.Millisecond)
		defer rl.Stop()

		identity := "wallet1"

		// Keep touching the bucket every 30ms for 180ms — never idle for 100ms
		for range 6 {
			require.NoError(t, rl.Allow(identity))
			time.Sleep(30 * time.Millisecond)
		}

		_, exists := rl.GetStats(identity)
		assert.True(t, exists, "active bucket should not be evicted")
	})

	t.Run("no eviction when idleTTL is zero", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 0, 0)
		defer rl.Stop()

		identity := "wallet1"
		require.NoError(t, rl.Allow(identity))

		time.Sleep(50 * time.Millisecond)

		_, exists := rl.GetStats(identity)
		assert.True(t, exists, "bucket should persist when eviction is disabled")
	})

	t.Run("Stop waits for goroutine", func(t *testing.T) {
		rl := NewRateLimiter(10, 10, 500*time.Millisecond, 100*time.Millisecond)
		// Stop must return promptly and not block
		done := make(chan struct{})
		go func() {
			rl.Stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop() did not return within timeout")
		}
	})
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// tokenBucket implements a token bucket rate limiter for a single identity
type tokenBucket struct {
	tokens         float64
	maxTokens      float64
	refillRate     float64 // tokens per second
	lastRefillTime time.Time
	lastSeen       time.Time // updated on every allow() call; used for idle eviction
	mu             sync.Mutex
}

// newTokenBucket creates a new token bucket with the specified capacity and refill rate
func newTokenBucket(maxTokens float64, refillRate float64) *tokenBucket {
	now := time.Now()
	return &tokenBucket{
		tokens:         maxTokens,
		maxTokens:      maxTokens,
		refillRate:     refillRate,
		lastRefillTime: now,
		lastSeen:       now,
	}
}

// allow checks if a request can proceed and consumes a token if so
func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefillTime).Seconds()

	// Refill tokens based on elapsed time
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefillTime = now
	tb.lastSeen = now

	// Check if we have tokens available
	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0

		return true
	}

	return false
}

// RateLimiter manages rate limits for multiple identities.
// Idle buckets (those not accessed for longer than idleTTL) are evicted
// by a background goroutine that runs every cleanupInterval.
type RateLimiter struct {
	buckets         map[string]*tokenBucket
	mu              sync.RWMutex
	maxTokens       float64 // burst capacity
	refillRate      float64 // tokens per second
	idleTTL         time.Duration
	cleanupInterval time.Duration
	cancel          context.CancelFunc
	cleanupDone     chan struct{}
}

// NewRateLimiter creates a new rate limiter with the specified parameters.
// requestsPerSecond: average number of requests allowed per second per identity.
// burstSize: maximum burst of requests allowed (should be >= requestsPerSecond).
// idleTTL: how long a bucket may be idle before it is evicted (0 = no eviction).
// cleanupInterval: how often the eviction sweep runs (0 = defaults to idleTTL/2).
func NewRateLimiter(requestsPerSecond float64, burstSize float64, idleTTL time.Duration, cleanupInterval time.Duration) *RateLimiter {
	if burstSize < requestsPerSecond {
		burstSize = requestsPerSecond
	}
	if idleTTL > 0 && cleanupInterval <= 0 {
		cleanupInterval = idleTTL / 2
	}

	ctx, cancel := context.WithCancel(context.Background())

	rl := &RateLimiter{
		buckets:         make(map[string]*tokenBucket),
		maxTokens:       burstSize,
		refillRate:      requestsPerSecond,
		idleTTL:         idleTTL,
		cleanupInterval: cleanupInterval,
		cancel:          cancel,
		cleanupDone:     make(chan struct{}),
	}

	if idleTTL > 0 {
		go rl.sweepLoop(ctx)
	} else {
		// No sweeping needed; signal done immediately so Stop() returns fast.
		close(rl.cleanupDone)
	}

	return rl
}

// Stop stops the background cleanup goroutine and waits for it to exit.
func (rl *RateLimiter) Stop() {
	rl.cancel()
	<-rl.cleanupDone
}

// Allow checks if a request from the given identity should be allowed.
// Returns nil if allowed, ErrRateLimitExceeded if the rate limit is exceeded.
func (rl *RateLimiter) Allow(identity string) error {
	if identity == "" {
		return errors.New("identity cannot be empty")
	}

	// Fast path: check if bucket exists
	rl.mu.RLock()
	bucket, exists := rl.buckets[identity]
	rl.mu.RUnlock()

	// Create bucket if it doesn't exist
	if !exists {
		rl.mu.Lock()
		// Double-check after acquiring write lock
		bucket, exists = rl.buckets[identity]
		if !exists {
			bucket = newTokenBucket(rl.maxTokens, rl.refillRate)
			rl.buckets[identity] = bucket
		}
		rl.mu.Unlock()
	}

	// Check if request is allowed
	if !bucket.allow() {
		return simple.ErrRateLimitExceeded
	}

	return nil
}

// Reset removes the rate limit state for a specific identity.
// Useful for testing or administrative operations.
func (rl *RateLimiter) Reset(identity string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, identity)
}

// ResetAll clears all rate limit state.
func (rl *RateLimiter) ResetAll() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.buckets = make(map[string]*tokenBucket)
}

// GetStats returns current statistics for an identity (for monitoring/debugging).
func (rl *RateLimiter) GetStats(identity string) (availableTokens float64, exists bool) {
	rl.mu.RLock()
	bucket, exists := rl.buckets[identity]
	rl.mu.RUnlock()

	if !exists {
		return 0, false
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Simulate refill to get current token count
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefillTime).Seconds()
	tokens := bucket.tokens + elapsed*bucket.refillRate
	if tokens > bucket.maxTokens {
		tokens = bucket.maxTokens
	}

	return tokens, true
}

// sweepLoop runs periodically and evicts buckets that have been idle for longer than idleTTL.
func (rl *RateLimiter) sweepLoop(ctx context.Context) {
	defer close(rl.cleanupDone)
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.evictIdle()
		}
	}
}

// evictIdle removes all buckets whose lastSeen is older than idleTTL.
func (rl *RateLimiter) evictIdle() {
	cutoff := time.Now().Add(-rl.idleTTL)

	// Collect candidates under read lock to minimise write-lock contention.
	rl.mu.RLock()
	var stale []string
	for id, bucket := range rl.buckets {
		bucket.mu.Lock()
		if bucket.lastSeen.Before(cutoff) {
			stale = append(stale, id)
		}
		bucket.mu.Unlock()
	}
	rl.mu.RUnlock()

	if len(stale) == 0 {
		return
	}

	rl.mu.Lock()
	for _, id := range stale {
		// Re-check under write lock: the bucket may have been accessed between
		// the RLock scan and acquiring the write lock.
		if b, ok := rl.buckets[id]; ok {
			b.mu.Lock()
			if b.lastSeen.Before(cutoff) {
				delete(rl.buckets, id)
			}
			b.mu.Unlock()
		}
	}
	rl.mu.Unlock()
}

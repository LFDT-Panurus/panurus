/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
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
	mu             sync.Mutex
}

// newTokenBucket creates a new token bucket with the specified capacity and refill rate
func newTokenBucket(maxTokens float64, refillRate float64) *tokenBucket {
	return &tokenBucket{
		tokens:         maxTokens,
		maxTokens:      maxTokens,
		refillRate:     refillRate,
		lastRefillTime: time.Now(),
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

	// Check if we have tokens available
	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0

		return true
	}

	return false
}

// RateLimiter manages rate limits for multiple identities
type RateLimiter struct {
	buckets    map[string]*tokenBucket
	mu         sync.RWMutex
	maxTokens  float64 // burst capacity
	refillRate float64 // tokens per second
}

// NewRateLimiter creates a new rate limiter with the specified parameters
// requestsPerSecond: average number of requests allowed per second per identity
// burstSize: maximum burst of requests allowed (should be >= requestsPerSecond)
func NewRateLimiter(requestsPerSecond float64, burstSize float64) *RateLimiter {
	if burstSize < requestsPerSecond {
		burstSize = requestsPerSecond
	}

	return &RateLimiter{
		buckets:    make(map[string]*tokenBucket),
		maxTokens:  burstSize,
		refillRate: requestsPerSecond,
	}
}

// Allow checks if a request from the given identity should be allowed
// Returns true if allowed, false if rate limit exceeded
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

// Reset removes the rate limit state for a specific identity
// Useful for testing or administrative operations
func (rl *RateLimiter) Reset(identity string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, identity)
}

// ResetAll clears all rate limit state
func (rl *RateLimiter) ResetAll() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.buckets = make(map[string]*tokenBucket)
}

// GetStats returns current statistics for an identity (for monitoring/debugging)
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

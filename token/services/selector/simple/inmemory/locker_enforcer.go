/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package inmemory

import (
	"sync"

	"github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// Locker enforces per-identity rate limiting and quota for token lock operations.
// Construct it with NewEnforcer and compose it into any locker implementation.
type Locker struct {
	rateLimiter         *RateLimiter
	identityLockCount   map[string]int
	maxLocksPerIdentity int
	mu                  sync.Mutex
}

// NewEnforcer builds a Locker from the given LockerConfig.
// It starts any background goroutines required by the rate limiter.
func NewEnforcer(config LockerConfig) *Locker {
	var rateLimiter *RateLimiter
	if config.RateLimit > 0 {
		rateLimiter = NewRateLimiter(config.RateLimit, config.RateLimitBurst, config.RateLimitIdleTTL, 0)
	}

	return &Locker{
		rateLimiter:         rateLimiter,
		identityLockCount:   map[string]int{},
		maxLocksPerIdentity: config.MaxLocksPerIdentity,
	}
}

// Stop shuts down any background goroutines (e.g. the rate-limiter sweep).
func (e *Locker) Stop() {
	if e.rateLimiter != nil {
		e.rateLimiter.Stop()
	}
}

// CheckRateLimit checks only the rate limit for the given identity.
// Returns nil if the request is permitted, or a wrapped ErrRateLimitExceeded otherwise.
// When identity is empty no check is applied.
func (e *Locker) CheckRateLimit(identity string) error {
	if identity == "" || e.rateLimiter == nil {
		return nil
	}

	if err := e.rateLimiter.Allow(identity); err != nil {
		return errors.Wrapf(simple.ErrRateLimitExceeded, "identity %s", identity)
	}

	return nil
}

// CheckQuota checks only the quota for the given identity.
// Must be called while the caller holds any lock that serialises lock acquisition,
// so that the check and the subsequent TrackLock are atomic with respect to other
// concurrent lock operations.
// Returns nil if the request is permitted, or a wrapped ErrQuotaExceeded otherwise.
// When identity is empty no check is applied.
func (e *Locker) CheckQuota(identity string) error {
	if identity == "" || e.maxLocksPerIdentity <= 0 {
		return nil
	}

	e.mu.Lock()
	currentCount := e.identityLockCount[identity]
	e.mu.Unlock()
	if currentCount >= e.maxLocksPerIdentity {
		return errors.Wrapf(simple.ErrQuotaExceeded, "identity %s has %d locks (max %d)", identity, currentCount, e.maxLocksPerIdentity)
	}

	return nil
}

// TrackLock records that one additional lock has been acquired for identity.
// Must be called after a lock is successfully granted.
func (e *Locker) TrackLock(identity string) {
	if identity == "" {
		return
	}
	e.mu.Lock()
	e.identityLockCount[identity]++
	e.mu.Unlock()
}

// TrackUnlock records that one lock has been released for identity.
// Must be called when a lock is removed.
func (e *Locker) TrackUnlock(identity string) {
	if identity == "" {
		return
	}
	e.mu.Lock()
	e.identityLockCount[identity]--
	if e.identityLockCount[identity] <= 0 {
		delete(e.identityLockCount, identity)
	}
	e.mu.Unlock()
}

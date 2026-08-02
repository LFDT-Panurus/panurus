/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network

import (
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	selector "github.com/LFDT-Panurus/panurus/token/services/selector/simple"
	"github.com/LFDT-Panurus/panurus/token/services/selector/simple/inmemory"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
)

// RateLimitConfig configures the built-in per-wallet rate limiter that the
// LockerProvider wraps around each simple-selector locker.
type RateLimitConfig struct {
	// Enabled reports whether the built-in limiter should be wired in.
	Enabled bool
	// RequestsPerSecond is the per-wallet token-lock rate. One selection issues
	// one lock request per candidate token, and retries the scan when candidates
	// are locked by others, so this bounds lock requests and not transactions.
	RequestsPerSecond float64
	// Burst is the per-wallet burst capacity.
	Burst float64
	// IdleTTL is how long an idle per-wallet bucket is kept before eviction.
	IdleTTL time.Duration
}

// LockerProvider creates token lockers for the simple selector service.
// It manages transaction locking to prevent double-spending during token selection.
type LockerProvider struct {
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService]
	sleepTimeout           time.Duration
	validTxEvictionTimeout time.Duration
	rateLimit              RateLimitConfig
}

// NewLockerProvider creates a new locker provider with the given configuration.
// When rateLimit.Enabled is true, every locker created is wrapped with the shared
// built-in per-wallet rate limiter (one limiter per TMS).
func NewLockerProvider(
	ttxStoreServiceManager db.StoreServiceManager[*ttxdb.StoreService],
	sleepTimeout time.Duration,
	validTxEvictionTimeout time.Duration,
	rateLimit RateLimitConfig,
) *LockerProvider {
	return &LockerProvider{
		ttxStoreServiceManager: ttxStoreServiceManager,
		sleepTimeout:           sleepTimeout,
		validTxEvictionTimeout: validTxEvictionTimeout,
		rateLimit:              rateLimit,
	}
}

// New creates a locker for the specified network, channel, and namespace.
func (s *LockerProvider) New(network, channel, namespace string) (selector.Locker, error) {
	db, err := s.ttxStoreServiceManager.StoreServiceByTMSId(token.TMSID{
		Network:   network,
		Channel:   channel,
		Namespace: namespace,
	})
	if err != nil {
		return nil, err
	}

	locker := inmemory.NewLocker(db, s.sleepTimeout, s.validTxEvictionTimeout)
	if !s.rateLimit.Enabled {
		return locker, nil
	}

	// One limiter per TMS; its lifecycle is tied to the wrapped locker's Stop(),
	// which the simple SelectorService invokes on shutdown.
	limiter := ratelimit.NewTokenBucketRateLimiter(s.rateLimit.RequestsPerSecond, s.rateLimit.Burst, s.rateLimit.IdleTTL, 0)

	return selector.NewRateLimitedLocker(locker, limiter), nil
}

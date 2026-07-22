/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/selector/config"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokenlockdb"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	lazy2 "github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

type SelectorService struct {
	managerLazyCache lazy2.Provider[*token.ManagementService, token.SelectorManager]
	mu               sync.Mutex
	managers         []*Manager
	limiters         []ratelimit.Limiter
}

func NewService(
	fetcherProvider FetcherProvider,
	tokenLockStoreServiceManager tokenlockdb.StoreServiceManager,
	c ConfigProvider,
	metricsProvider metrics.Provider,
) *SelectorService {
	cfg, err := config.New(c)
	if err != nil {
		logger.Errorf("error getting selector config, using defaults. %s", err.Error())
	}

	svc := &SelectorService{}
	loader := &loader{
		tokenLockStoreServiceManager: tokenLockStoreServiceManager,
		fetcherProvider:              fetcherProvider,
		retryInterval:                cfg.GetRetryInterval(),
		numRetries:                   cfg.GetNumRetries(),
		leaseExpiry:                  cfg.GetLeaseExpiry(),
		leaseCleanupTickPeriod:       cfg.GetLeaseCleanupTickPeriod(),
		rateLimitEnabled:             cfg.RateLimitEnabled(),
		rateLimit:                    cfg.GetRateLimit(),
		rateLimitBurst:               cfg.GetRateLimitBurst(),
		rateLimitIdleTTL:             cfg.GetRateLimitIdleTTL(),
		metrics:                      NewMetrics(metricsProvider),
		onCreate:                     svc.trackManager,
		onLimiterCreated:             svc.trackLimiter,
	}
	svc.managerLazyCache = lazy2.NewProviderWithKeyMapper(key, loader.load)

	return svc
}

func (s *SelectorService) SelectorManager(tms *token.ManagementService) (token.SelectorManager, error) {
	if tms == nil {
		return nil, errors.Errorf("invalid tms, nil reference")
	}

	return s.managerLazyCache.Get(tms)
}

// Shutdown stops all background goroutines for every manager created by this
// service, and stops the built-in rate limiters it owns.
func (s *SelectorService) Shutdown() {
	s.mu.Lock()
	managers := s.managers
	limiters := s.limiters
	s.managers = nil
	s.limiters = nil
	s.mu.Unlock()

	for _, m := range managers {
		if err := m.Stop(); err != nil {
			logger.Errorf("error shutting down sherdlock service manager: %s", err)
		}
	}
	for _, l := range limiters {
		l.Stop()
	}
}

func (s *SelectorService) trackManager(m *Manager) {
	s.mu.Lock()
	s.managers = append(s.managers, m)
	s.mu.Unlock()
}

func (s *SelectorService) trackLimiter(l ratelimit.Limiter) {
	s.mu.Lock()
	s.limiters = append(s.limiters, l)
	s.mu.Unlock()
}

func (s *SelectorService) ManagersCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.managers)
}

type loader struct {
	tokenLockStoreServiceManager tokenlockdb.StoreServiceManager
	fetcherProvider              FetcherProvider
	numRetries                   int
	retryInterval                time.Duration
	leaseExpiry                  time.Duration
	leaseCleanupTickPeriod       time.Duration
	rateLimitEnabled             bool
	rateLimit                    float64
	rateLimitBurst               float64
	rateLimitIdleTTL             time.Duration
	metrics                      *Metrics
	onCreate                     func(*Manager)
	onLimiterCreated             func(ratelimit.Limiter)
}

func (s *loader) load(tms *token.ManagementService) (token.SelectorManager, error) {
	return s.loadTMS(tms)
}

func (s *loader) loadTMS(tms TMS) (token.SelectorManager, error) {
	pp := tms.PublicParameters()
	if pp == nil {
		return nil, errors.Errorf("public parameters not set yet for TMS [%s]", tms.ID())
	}
	tokenLockStoreService, err := s.tokenLockStoreServiceManager.StoreServiceByTMSId(tms.ID())
	if err != nil {
		return nil, errors.Errorf("failed to create tokenLockDB: %v", err)
	}
	fetcher, err := s.fetcherProvider.GetFetcher(tms.ID())
	if err != nil {
		return nil, errors.Errorf("failed to create token fetcher: %v", err)
	}

	// Wrap the lock store with the built-in per-wallet rate limiter (on by default,
	// disabled via a negative token.selector.rateLimit). The limiter is per TMS and
	// its lifecycle is owned by the service (see trackLimiter/Shutdown).
	var locker Locker = tokenLockStoreService
	if s.rateLimitEnabled {
		limiter := ratelimit.NewTokenBucketRateLimiter(s.rateLimit, s.rateLimitBurst, s.rateLimitIdleTTL, 0)
		if s.onLimiterCreated != nil {
			s.onLimiterCreated(limiter)
		}
		locker = NewRateLimitedLocker(tokenLockStoreService, limiter)
	}

	mgr := NewManager(
		fetcher,
		locker,
		pp.Precision(),
		s.retryInterval,
		s.numRetries,
		s.leaseExpiry,
		s.leaseCleanupTickPeriod,
		s.metrics,
	)
	if s.onCreate != nil {
		s.onCreate(mgr)
	}

	return mgr, nil
}

func key(tms *token.ManagementService) string {
	return tms.ID().String()
}

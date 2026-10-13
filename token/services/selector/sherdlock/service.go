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

// lazyCache is the per-TMS manager cache a SelectorService looks managers up in.
type lazyCache = lazy2.Provider[*token.ManagementService, token.SelectorManager]

type SelectorService struct {
	// cacheMu guards managerLazyCache. SelectorManager holds it for reading across the whole lookup
	// and Shutdown holds it for writing to replace the cache, so a lookup can never finish on a cache
	// Shutdown has already discarded and hand out a manager it has stopped. It is a separate lock
	// from mu because building a manager inside the lookup calls trackManager, which takes mu.
	cacheMu          sync.RWMutex
	managerLazyCache lazyCache
	newCache         func() lazyCache
	mu               sync.Mutex
	managers         []*Manager
}

// NewService returns a SelectorService for the sherdlock driver.
//
// By default, selection is not rate limited. Passing ratelimit options, or enabling the
// token.selector.rateLimit* configuration keys, meters every selection request per wallet.
func NewService(
	fetcherProvider FetcherProvider,
	tokenLockStoreServiceManager tokenlockdb.StoreServiceManager,
	c ConfigProvider,
	metricsProvider metrics.Provider,
	opts ...ratelimit.Option,
) *SelectorService {
	cfg, err := config.New(c)
	if err != nil {
		logger.Errorf("error getting selector config, using defaults. %s", err.Error())
		cfg = &config.Config{}
	}

	svc := &SelectorService{}
	loader := &loader{
		tokenLockStoreServiceManager: tokenLockStoreServiceManager,
		fetcherProvider:              fetcherProvider,
		retryInterval:                cfg.GetRetryInterval(),
		numRetries:                   cfg.GetNumRetries(),
		leaseExpiry:                  cfg.GetLeaseExpiry(),
		leaseCleanupTickPeriod:       cfg.GetLeaseCleanupTickPeriod(),
		metrics:                      NewMetrics(metricsProvider),
		limiter:                      ratelimit.CompileOptions(opts...).Limiter(cfg),
		onCreate:                     svc.trackManager,
	}
	if loader.limiter != nil {
		logger.Infof("per-wallet token selection rate limiting is enabled")
	}
	svc.newCache = func() lazyCache {
		return lazy2.NewProviderWithKeyMapper(key, loader.load)
	}
	svc.managerLazyCache = svc.newCache()

	return svc
}

func (s *SelectorService) SelectorManager(tms *token.ManagementService) (token.SelectorManager, error) {
	if tms == nil {
		return nil, errors.Errorf("invalid tms, nil reference")
	}

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	return s.managerLazyCache.Get(tms)
}

// Shutdown stops all background goroutines for every manager created by this service.
//
// It deliberately leaves the rate limiter alone. Shutdown also runs on routine public-parameter
// reloads (see token.ManagementServiceProvider.Update), after which the service keeps serving
// managers: resetting the wallet allowances there would let a throttled client wash out its debt
// by triggering a reload, and a limiter supplied through ratelimit.WithLimiter belongs to the
// caller in the first place. The built-in limiter runs no goroutines and prunes its own buckets,
// so there is nothing to leak.
func (s *SelectorService) Shutdown() {
	// Start from an empty cache. The cached managers are the ones being stopped here, and the
	// cache is keyed by TMS id, so keeping it would hand every later caller the same stopped
	// manager for as long as the process lives: its background cleanup would never run again.
	//
	// Taking cacheMu first waits out any lookup in flight, so every manager the old cache built is
	// already tracked, and is stopped below, before the old cache is dropped.
	s.cacheMu.Lock()
	if s.newCache != nil {
		s.managerLazyCache = s.newCache()
	}
	s.mu.Lock()
	managers := s.managers
	s.managers = nil
	s.mu.Unlock()
	s.cacheMu.Unlock()

	for _, m := range managers {
		if err := m.Stop(); err != nil {
			logger.Errorf("error shutting down sherdlock service manager: %s", err)
		}
	}
}

func (s *SelectorService) trackManager(m *Manager) {
	s.mu.Lock()
	s.managers = append(s.managers, m)
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
	metrics                      *Metrics
	// limiter meters selection requests per wallet. It is nil when rate limiting is
	// disabled, which is the default, and is shared by every manager the loader builds.
	limiter  ratelimit.Limiter
	onCreate func(*Manager)
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

	mgr := NewManager(
		fetcher,
		tokenLockStoreService,
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

	// Decorate returns mgr unchanged when no limiter is configured.
	return ratelimit.Decorate(mgr, s.limiter, tms.ID().String()), nil
}

func key(tms *token.ManagementService) string {
	return tms.ID().String()
}

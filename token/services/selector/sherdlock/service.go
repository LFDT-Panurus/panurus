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

// Option customizes a SelectorService.
type Option func(*serviceOptions)

// serviceOptions collects the optional settings of NewService.
type serviceOptions struct {
	limiter    ratelimit.Limiter
	limiterSet bool
}

// WithLimiter installs an application-supplied rate limiter for token selection, in place
// of whatever the configuration selects. Passing a nil Limiter disables rate limiting
// altogether, which is what an application whose own throttling already lives in its
// Locker wants. To switch the built-in limiter on from code, pass ratelimit.NewDefault().
//
// The application keeps ownership of a limiter passed here: Shutdown does not call Stop on
// it, because Shutdown also runs on public-parameters updates rather than only at process
// exit. Limiters that own resources must be stopped by the application.
func WithLimiter(limiter ratelimit.Limiter) Option {
	return func(o *serviceOptions) {
		o.limiter = limiter
		o.limiterSet = true
	}
}

type SelectorService struct {
	managerLazyCache lazy2.Provider[*token.ManagementService, token.SelectorManager]
	mu               sync.Mutex
	managers         []*Manager
	// limiter throttles selections per wallet. It is nil when rate limiting is disabled.
	limiter ratelimit.Limiter
	// ownsLimiter records whether this service created limiter from the configuration, and
	// is therefore responsible for stopping it. A limiter installed with WithLimiter belongs
	// to the application and is never stopped here.
	ownsLimiter bool
}

// NewService returns a selector service for the sherdlock driver. Selections are not rate
// limited unless the configuration switches the built-in per-wallet limiter on (see the
// ratelimit package and token.selector's rateLimitEnabled) or WithLimiter supplies one.
func NewService(
	fetcherProvider FetcherProvider,
	tokenLockStoreServiceManager tokenlockdb.StoreServiceManager,
	c ConfigProvider,
	metricsProvider metrics.Provider,
	opts ...Option,
) *SelectorService {
	cfg, err := config.New(c)
	if err != nil {
		logger.Errorf("error getting selector config, using defaults. %s", err.Error())
	}

	o := &serviceOptions{}
	for _, opt := range opts {
		opt(o)
	}
	limiter := o.limiter
	if !o.limiterSet {
		limiter = ratelimit.FromConfig(cfg)
	}

	svc := &SelectorService{limiter: limiter, ownsLimiter: !o.limiterSet}
	loader := &loader{
		limiter:                      limiter,
		tokenLockStoreServiceManager: tokenLockStoreServiceManager,
		fetcherProvider:              fetcherProvider,
		retryInterval:                cfg.GetRetryInterval(),
		numRetries:                   cfg.GetNumRetries(),
		leaseExpiry:                  cfg.GetLeaseExpiry(),
		leaseCleanupTickPeriod:       cfg.GetLeaseCleanupTickPeriod(),
		metrics:                      NewMetrics(metricsProvider),
		onCreate:                     svc.trackManager,
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

// Shutdown stops all background goroutines for every manager created by this service.
//
// It also stops the rate limiter, but only one this service built from the configuration: a
// limiter installed with WithLimiter belongs to the application, and Shutdown is not only a
// process-exit hook - ManagementServiceProvider.Update calls it whenever the public
// parameters of a TMS change, mid-process. Stopping the application's limiter there would
// release resources it is still using (a shared Redis client, say) while the wrapped
// managers keep calling Allow on it.
func (s *SelectorService) Shutdown() {
	s.mu.Lock()
	managers := s.managers
	s.managers = nil
	limiter := s.limiter
	ownsLimiter := s.ownsLimiter
	s.mu.Unlock()

	if limiter != nil && ownsLimiter {
		limiter.Stop()
	}

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
	onCreate                     func(*Manager)
	limiter                      ratelimit.Limiter
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

	// Metering sits outside the manager so that it is charged once per selection request
	// rather than once per token lock: a single selection locks every candidate it walks.
	return ratelimit.NewSelectorManager(mgr, s.limiter, tms.ID().String()), nil
}

func key(tms *token.ManagementService) string {
	return tms.ID().String()
}

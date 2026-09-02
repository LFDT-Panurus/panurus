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

	// Validate configuration. This is the default selector driver, so a
	// rejected configuration would otherwise be applied silently, with no
	// warning at all.
	if err := cfg.Validate(); err != nil {
		logger.Errorf("invalid selector configuration: %s, using defaults", err.Error())
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
		maxTokensPerSelection:        cfg.GetMaxTokensPerSelection(),
		maxLockAttempts:              cfg.GetMaxLockAttempts(),
		maxLocksPerTx:                cfg.GetMaxLocksPerTransaction(),
		selectionTimeout:             cfg.GetSelectionTimeout(),
		metrics:                      NewMetrics(metricsProvider),
		limiter:                      ratelimit.CompileOptions(opts...).Limiter(cfg),
		onCreate:                     svc.trackManager,
	}
	if loader.limiter != nil {
		logger.Infof("per-wallet token selection rate limiting is enabled")
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
// It deliberately leaves the rate limiter alone. Shutdown also runs on routine public-parameter
// reloads (see token.ManagementServiceProvider.Update), after which the service keeps serving
// managers: resetting the wallet allowances there would let a throttled client wash out its debt
// by triggering a reload, and a limiter supplied through ratelimit.WithLimiter belongs to the
// caller in the first place. The built-in limiter runs no goroutines and prunes its own buckets,
// so there is nothing to leak.
func (s *SelectorService) Shutdown() {
	s.mu.Lock()
	managers := s.managers
	s.managers = nil
	s.mu.Unlock()

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
	maxTokensPerSelection        int
	maxLockAttempts              int
	maxLocksPerTx                int
	selectionTimeout             time.Duration
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

	mgr := NewManager(&Config{
		Fetcher:                fetcher,
		Locker:                 NewBoundedLocker(tokenLockStoreService, s.maxLocksPerTx),
		Precision:              pp.Precision(),
		Backoff:                s.retryInterval,
		MaxRetriesAfterBackOff: s.numRetries,
		LeaseExpiry:            s.leaseExpiry,
		LeaseCleanupTickPeriod: s.leaseCleanupTickPeriod,
		MaxTokensPerSelection:  s.maxTokensPerSelection,
		MaxLockAttempts:        s.maxLockAttempts,
		SelectionTimeout:       s.selectionTimeout,
		Metrics:                s.metrics,
	})
	if s.onCreate != nil {
		s.onCreate(mgr)
	}

	// Decorate returns mgr unchanged when no limiter is configured.
	return ratelimit.Decorate(mgr, s.limiter, tms.ID().String()), nil
}

func key(tms *token.ManagementService) string {
	return tms.ID().String()
}

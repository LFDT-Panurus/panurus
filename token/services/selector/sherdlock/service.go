/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package sherdlock

import (
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
	managerLazyCache lazy2.Provider[*token.ManagementService, *managerEntry]
}

// managerEntry is the cached manager of one TMS: the manager itself, and what the service hands
// out for it, which is the manager decorated by the rate limiter when one is configured.
type managerEntry struct {
	manager  *Manager
	selector token.SelectorManager
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
		limiter:                      ratelimit.CompileOptions(opts...).Limiter(cfg, metricsProvider),
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

	entry, err := s.managerLazyCache.Get(tms)
	if err != nil {
		return nil, err
	}
	// The cache is keyed by TMS id, so after a public parameters update the TMS gets back the
	// manager built for the previous parameters. Keep its precision in line with the current ones.
	if pp := tms.PublicParameters(); pp != nil {
		entry.manager.SetPrecision(pp.Precision())
	}

	return entry.selector, nil
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
	limiter ratelimit.Limiter
}

func (s *loader) load(tms *token.ManagementService) (*managerEntry, error) {
	return s.loadTMS(tms)
}

func (s *loader) loadTMS(tms TMS) (*managerEntry, error) {
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
	// Decorate returns mgr unchanged when no limiter is configured.
	return &managerEntry{manager: mgr, selector: ratelimit.Decorate(mgr, s.limiter, tms.ID().String())}, nil
}

func key(tms *token.ManagementService) string {
	return tms.ID().String()
}

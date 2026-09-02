/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/selector/config"
	"github.com/LFDT-Panurus/panurus/token/services/selector/ratelimit"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
)

var logger = logging.MustGetLogger()

type ConfigProvider interface {
	UnmarshalKey(key string, rawVal any) error
}

type LockerProvider interface {
	New(network, channel, namespace string) (Locker, error)
}

// stoppable is implemented by lockers that have a lifecycle (e.g. inmemory.locker).
type stoppable interface {
	Stop() error
}

type SelectorService struct {
	managerLazyCache lazy.Provider[*token.ManagementService, token.SelectorManager]
	mu               sync.Mutex
	lockers          []stoppable
}

// NewService returns a SelectorService for the simple driver.
//
// By default, selection is not rate limited. Passing ratelimit options, or enabling the
// token.selector.rateLimit* configuration keys, meters every selection request per wallet.
func NewService(lockerProvider LockerProvider, c ConfigProvider, opts ...ratelimit.Option) *SelectorService {
	cfg, err := config.New(c)
	if err != nil {
		logger.Errorf("error getting selector config, using defaults. %s", err.Error())
		cfg = &config.Config{}
	}

	// Validate configuration. A rejected configuration must actually fall back
	// to defaults: applying the values we just reported as invalid is worse
	// than ignoring them (e.g. maxLockAttempts below maxTokensPerSelection
	// aborts every selection partway through).
	if err := cfg.Validate(); err != nil {
		logger.Errorf("invalid selector configuration: %s, using defaults", err.Error())
		cfg = &config.Config{}
	}

	limits := cfg.GetLimits()

	svc := &SelectorService{}
	loader := &loader{
		lockerProvider:        lockerProvider,
		maxRetries:            limits.MaxRetries,
		retryInterval:         cfg.GetRetryInterval(),
		requestCertification:  true,
		limiter:               ratelimit.CompileOptions(opts...).Limiter(cfg),
		onLockerCreated:       svc.trackLocker,
		maxTokensPerSelection: limits.MaxTokensPerSelection,
		maxLockAttempts:       limits.MaxLockAttempts,
		selectionTimeout:      limits.SelectionTimeout,
	}
	if loader.limiter != nil {
		logger.Infof("per-wallet token selection rate limiting is enabled")
	}
	svc.managerLazyCache = lazy.NewProviderWithKeyMapper(key, loader.load)

	return svc
}

func (s *SelectorService) SelectorManager(tms *token.ManagementService) (token.SelectorManager, error) {
	if tms == nil {
		return nil, errors.Errorf("invalid tms, nil reference")
	}

	return s.managerLazyCache.Get(tms)
}

// Shutdown stops all background goroutines for every locker created by this service.
//
// It deliberately leaves the rate limiter alone. Shutdown also runs on routine public-parameter
// reloads (see token.ManagementServiceProvider.Update), after which the service keeps serving
// managers: resetting the wallet allowances there would let a throttled client wash out its debt
// by triggering a reload, and a limiter supplied through ratelimit.WithLimiter belongs to the
// caller in the first place. The built-in limiter runs no goroutines and prunes its own buckets,
// so there is nothing to leak.
func (s *SelectorService) Shutdown() {
	s.mu.Lock()
	lockers := s.lockers
	s.lockers = nil
	s.mu.Unlock()

	for _, l := range lockers {
		if err := l.Stop(); err != nil {
			logger.Warnf("failed stopping locker: %s", err)
		}
	}
}

func (s *SelectorService) trackLocker(l Locker) {
	if st, ok := l.(stoppable); ok {
		s.mu.Lock()
		s.lockers = append(s.lockers, st)
		s.mu.Unlock()
	}
}

type Cache interface {
	Get(key string) (any, bool)
	Add(key string, value any)
}

type queryService struct {
	qe     QueryService
	locker Locker
}

func (q *queryService) UnspentTokensIterator(ctx context.Context) (*token.UnspentTokensIterator, error) {
	return q.qe.UnspentTokensIterator(ctx)
}

func (q *queryService) UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type, limit int) (driver.UnspentTokensIterator, error) {
	return q.qe.UnspentTokensIteratorBy(ctx, id, tokenType, limit)
}

func (q *queryService) GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error) {
	return q.qe.GetTokens(ctx, inputs...)
}

type loader struct {
	lockerProvider       LockerProvider
	maxRetries           int
	retryInterval        time.Duration
	requestCertification bool
	// limiter meters selection requests per wallet. It is nil when rate limiting is
	// disabled, which is the default, and is shared by every manager the loader builds.
	limiter         ratelimit.Limiter
	onLockerCreated func(Locker)

	// Resource limits
	maxTokensPerSelection int
	maxLockAttempts       int
	selectionTimeout      time.Duration
}

func (s *loader) load(tms *token.ManagementService) (token.SelectorManager, error) {
	logger.Debugf("new in-memory locker for [%s:%s:%s]", tms.Network(), tms.Channel(), tms.Namespace())

	locker, err := s.lockerProvider.New(tms.Network(), tms.Channel(), tms.Namespace())
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting locker")
	}
	if s.onLockerCreated != nil {
		s.onLockerCreated(locker)
	}
	qe := &queryService{
		qe:     tms.Vault().NewQueryEngine(),
		locker: locker,
	}

	return s.newManager(locker, qe, tms.PublicParametersManager().PublicParameters().Precision(), tms.ID().String()), nil
}

// newManager builds the manager for one TMS and, when rate limiting is enabled, wraps it so that
// every selection request is metered against the allowance of the wallet it selects for. scope is
// the TMS id, which keeps the allowances of one network or namespace separate from the others.
func (s *loader) newManager(locker Locker, qs QueryService, precision uint64, scope string) token.SelectorManager {
	mgr := NewManager(
		locker,
		func() QueryService { return qs },
		s.maxRetries,
		s.retryInterval,
		s.requestCertification,
		precision,
		s.maxTokensPerSelection,
		s.maxLockAttempts,
		s.selectionTimeout,
	)

	// Decorate returns mgr unchanged when no limiter is configured, which is the default.
	return ratelimit.Decorate(mgr, s.limiter, scope)
}

func key(tms *token.ManagementService) string {
	return tms.Network() + tms.Channel() + tms.Namespace()
}

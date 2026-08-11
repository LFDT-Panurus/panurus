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
	managerLazyCache lazy.Provider[*token.ManagementService, token.SelectorManager]
	mu               sync.Mutex
	lockers          []stoppable
	// limiter throttles selections per wallet. It is nil when rate limiting is disabled.
	limiter ratelimit.Limiter
	// ownsLimiter records whether this service created limiter from the configuration, and
	// is therefore responsible for stopping it. A limiter installed with WithLimiter belongs
	// to the application and is never stopped here.
	ownsLimiter bool
}

// NewService returns a selector service for the simple driver. Selections are not rate
// limited unless the configuration switches the built-in per-wallet limiter on (see the
// ratelimit package and token.selector's rateLimitEnabled) or WithLimiter supplies one.
func NewService(lockerProvider LockerProvider, c ConfigProvider, opts ...Option) *SelectorService {
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
		lockerProvider:       lockerProvider,
		numRetries:           cfg.GetNumRetries(),
		retryInterval:        cfg.GetRetryInterval(),
		requestCertification: true,
		onLockerCreated:      svc.trackLocker,
		limiter:              limiter,
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
// It also stops the rate limiter, but only one this service built from the configuration: a
// limiter installed with WithLimiter belongs to the application, and Shutdown is not only a
// process-exit hook - ManagementServiceProvider.Update calls it whenever the public
// parameters of a TMS change, mid-process. Stopping the application's limiter there would
// release resources it is still using (a shared Redis client, say) while the wrapped
// managers keep calling Allow on it.
func (s *SelectorService) Shutdown() {
	s.mu.Lock()
	lockers := s.lockers
	s.lockers = nil
	limiter := s.limiter
	ownsLimiter := s.ownsLimiter
	s.mu.Unlock()

	if limiter != nil && ownsLimiter {
		limiter.Stop()
	}

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

func (q *queryService) UnspentTokensIteratorBy(ctx context.Context, id string, tokenType token2.Type) (driver.UnspentTokensIterator, error) {
	return q.qe.UnspentTokensIteratorBy(ctx, id, tokenType)
}

func (q *queryService) GetTokens(ctx context.Context, inputs ...*token2.ID) ([]*token2.Token, error) {
	return q.qe.GetTokens(ctx, inputs...)
}

type loader struct {
	lockerProvider       LockerProvider
	numRetries           int
	retryInterval        time.Duration
	requestCertification bool
	onLockerCreated      func(Locker)
	limiter              ratelimit.Limiter
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

	mgr := NewManager(
		locker,
		func() QueryService { return qe },
		s.numRetries,
		s.retryInterval,
		s.requestCertification,
		tms.PublicParametersManager().PublicParameters().Precision(),
	)

	// Metering sits outside the manager so that it is charged once per selection request
	// rather than once per token lock: a single selection locks every candidate it walks.
	return ratelimit.NewSelectorManager(mgr, s.limiter, tms.ID().String()), nil
}

func key(tms *token.ManagementService) string {
	return tms.Network() + tms.Channel() + tms.Namespace()
}

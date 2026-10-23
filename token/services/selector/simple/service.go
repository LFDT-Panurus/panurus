/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package simple

import (
	"context"
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

type SelectorService struct {
	managerLazyCache lazy.Provider[*token.ManagementService, *managerEntry]
}

// managerEntry is the cached manager of one TMS: the manager itself, and what the service hands
// out for it, which is the manager decorated by the rate limiter when one is configured.
type managerEntry struct {
	manager  *Manager
	selector token.SelectorManager
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

	svc := &SelectorService{}
	loader := &loader{
		lockerProvider:       lockerProvider,
		numRetries:           cfg.GetNumRetries(),
		retryInterval:        cfg.GetRetryInterval(),
		requestCertification: true,
		limiter:              ratelimit.CompileOptions(opts...).Limiter(cfg),
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

	entry, err := s.managerLazyCache.Get(tms)
	if err != nil {
		return nil, err
	}
	// The cache is keyed by TMS, so after a public parameters update the TMS gets back the
	// manager built for the previous parameters. Keep its precision in line with the current ones.
	if pp := tms.PublicParameters(); pp != nil {
		entry.manager.SetPrecision(pp.Precision())
	}

	return entry.selector, nil
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
	// limiter meters selection requests per wallet. It is nil when rate limiting is
	// disabled, which is the default, and is shared by every manager the loader builds.
	limiter ratelimit.Limiter
}

func (s *loader) load(tms *token.ManagementService) (*managerEntry, error) {
	logger.Debugf("new in-memory locker for [%s:%s:%s]", tms.Network(), tms.Channel(), tms.Namespace())

	locker, err := s.lockerProvider.New(tms.Network(), tms.Channel(), tms.Namespace())
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting locker")
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
func (s *loader) newManager(locker Locker, qs QueryService, precision uint64, scope string) *managerEntry {
	mgr := NewManager(
		locker,
		func() QueryService { return qs },
		s.numRetries,
		s.retryInterval,
		s.requestCertification,
		precision,
	)

	// Decorate returns mgr unchanged when no limiter is configured, which is the default.
	return &managerEntry{manager: mgr, selector: ratelimit.Decorate(mgr, s.limiter, scope)}
}

func key(tms *token.ManagementService) string {
	return tms.Network() + tms.Channel() + tms.Namespace()
}

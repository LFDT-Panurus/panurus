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

func NewService(lockerProvider LockerProvider, c ConfigProvider) *SelectorService {
	cfg, err := config.New(c)
	if err != nil {
		logger.Errorf("error getting selector config, using defaults. %s", err.Error())
	}

	svc := &SelectorService{}
	loader := &loader{
		lockerProvider:       lockerProvider,
		numRetries:           cfg.GetNumRetries(),
		retryInterval:        cfg.GetRetryInterval(),
		requestCertification: true,
		onLockerCreated:      svc.trackLocker,
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

	return NewManager(
		locker,
		func() QueryService { return qe },
		s.numRetries,
		s.retryInterval,
		s.requestCertification,
		tms.PublicParametersManager().PublicParameters().Precision(),
	), nil
}

func key(tms *token.ManagementService) string {
	return tms.Network() + tms.Channel() + tms.Namespace()
}

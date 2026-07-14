/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

// StoreServiceManager manages transaction store services for different TMS instances.
type StoreServiceManager = ttxdb.StoreServiceManager

// TokensServiceManager manages token services for different TMS instances.
type TokensServiceManager services.ServiceManager[*tokens.Service]

// CheckServiceProvider provides health check services for transaction and token databases.
type CheckServiceProvider interface {
	CheckService(id token.TMSID, adb *ttxdb.StoreService, tdb *tokens.Service) (CheckService, error)
}

// ServiceManager handles the services
type ServiceManager struct {
	p lazy.Provider[token.TMSID, *Service]

	networkProvider      dep.NetworkProvider
	tokensServiceManager TokensServiceManager
}

// NewServiceManager creates a new Service manager.
func NewServiceManager(
	networkProvider dep.NetworkProvider,
	tmsProvider dep.TokenManagementServiceProvider,
	ttxStoreServiceManager StoreServiceManager,
	tokensServiceManager TokensServiceManager,
	tracerProvider trace.TracerProvider,
	metricsProvider metrics.Provider,
	checkServiceProvider CheckServiceProvider,
) *ServiceManager {
	return &ServiceManager{
		p: lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (*Service, error) {
			ttxStoreService, err := ttxStoreServiceManager.StoreServiceByTMSId(tmsID)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get ttxdb for [%s]", tmsID)
			}
			tokensService, err := tokensServiceManager.ServiceByTMSId(tmsID)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get ttxdb for [%s]", tmsID)
			}
			checkService, err := checkServiceProvider.CheckService(tmsID, ttxStoreService, tokensService)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get checkservice for [%s]", tmsID)
			}
			wrapper := &Service{
				networkProvider: networkProvider,
				tmsID:           tmsID,
				tmsProvider:     tmsProvider,
				ttxStoreService: ttxStoreService,
				tokensService:   tokensService,
				finalityTracer: tracerProvider.Tracer("db", tracing.WithMetricsOpts(tracing.MetricsOpts{
					LabelNames: []tracing.LabelName{txIdLabel},
				})),
				metricsProvider: metricsProvider,
				checkService:    checkService,
			}
			_, err = networkProvider.GetNetwork(tmsID.Network, tmsID.Channel)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get network instance for [%s:%s]", tmsID.Network, tmsID.Channel)
			}

			return wrapper, nil
		}),
		networkProvider:      networkProvider,
		tokensServiceManager: tokensServiceManager,
	}
}

// ServiceByTMSId returns the Service for the given TMS
func (m *ServiceManager) ServiceByTMSId(tmsID token.TMSID) (*Service, error) {
	return m.p.Get(tmsID)
}

// CacheRequest stores the request's details for later use.
func (m *ServiceManager) CacheRequest(ctx context.Context, tmsID token.TMSID, request *token.Request) error {
	service, err := m.tokensServiceManager.ServiceByTMSId(tmsID)
	if err != nil {
		return errors.WithMessagef(err, "failed to get service for [%s]", tmsID)
	}

	return service.CacheRequest(ctx, request)
}

var managerType = reflect.TypeFor[*ServiceManager]()

// Get returns the Service instance for the passed TMS
func Get(sp token.ServiceProvider, tms dep.TokenManagementService) *Service {
	if tms == nil {
		logger.Debugf("no TMS provided")

		return nil
	}
	s, err := sp.GetService(managerType)
	if err != nil {
		logger.Errorf("failed to get manager service: [%s]", err)

		return nil
	}
	auditor, err := s.(*ServiceManager).ServiceByTMSId(tms.ID())
	if err != nil {
		logger.Errorf("failed to get db for TMS [%s]: [%s]", tms.ID(), err)

		return nil
	}

	return auditor
}

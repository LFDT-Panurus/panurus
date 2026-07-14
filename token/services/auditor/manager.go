/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package auditor

import (
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/storage/auditdb"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/LFDT-Panurus/panurus/token/services/ttx/dep"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/tracing"
	"go.opentelemetry.io/otel/trace"
)

//go:generate counterfeiter -o mock/check_service_provider.go -fake-name CheckServiceProvider . CheckServiceProvider
//go:generate counterfeiter -o mock/tokens_service_manager.go -fake-name TokensServiceManager . TokensServiceManager

// TokenManagementServiceProvider provides access to token management services.
type TokenManagementServiceProvider interface {
	GetManagementService(opts ...token.ServiceOption) (*token.ManagementService, error)
}

// StoreServiceManager manages audit database store services.
type StoreServiceManager = auditdb.StoreServiceManager

// TokensServiceManager manages token services.
type TokensServiceManager = services.ServiceManager[*tokens.Service]

// CheckServiceProvider provides check services for auditor validation.
type CheckServiceProvider interface {
	CheckService(id token.TMSID, adb *auditdb.StoreService, tdb *tokens.Service) (CheckService, error)
}

// ServiceManager handles the services
type ServiceManager struct {
	p lazy.Provider[token.TMSID, *Service]

	networkProvider     NetworkProvider
	tokenServiceManager TokensServiceManager
	tmsProvider         dep.TokenManagementServiceProvider
	configService       *config.Service
}

// NewServiceManager creates a new Service manager.
func NewServiceManager(
	networkProvider NetworkProvider,
	auditStoreServiceManager StoreServiceManager,
	tokensServiceManager TokensServiceManager,
	tmsProvider dep.TokenManagementServiceProvider,
	tracerProvider trace.TracerProvider,
	metricsProvider metrics.Provider,
	checkServiceProvider CheckServiceProvider,
	configService *config.Service,
) *ServiceManager {
	return &ServiceManager{
		p: lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (*Service, error) {
			auditDB, err := auditStoreServiceManager.StoreServiceByTMSId(tmsID)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get auditdb for [%s]", tmsID)
			}
			tokenDB, err := tokensServiceManager.ServiceByTMSId(tmsID)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get auditdb for [%s]", tmsID)
			}
			_, err = networkProvider.GetNetwork(tmsID.Network, tmsID.Channel)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get network instance for [%s]", tmsID)
			}
			checkService, err := checkServiceProvider.CheckService(tmsID, auditDB, tokenDB)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get checkservice for [%s]", tmsID)
			}

			// Load lock configuration from config service
			var lockConfig *LockConfig
			if configService != nil {
				if tmsConfig, err := configService.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace); err == nil {
					lockConfig = LoadLockConfigFromConfiguration(tmsConfig)
				} else {
					logger.Warnf("failed to get configuration for [%s], using default lock config: %v", tmsID, err)
					lockConfig = DefaultLockConfig()
				}
			} else {
				lockConfig = DefaultLockConfig()
			}

			auditor := &Service{
				networkProvider: networkProvider,
				tmsID:           tmsID,
				auditDB:         auditDB,
				tokenDB:         tokenDB,
				tmsProvider:     tmsProvider,
				finalityTracer: tracerProvider.Tracer("auditor", tracing.WithMetricsOpts(tracing.MetricsOpts{
					LabelNames: []tracing.LabelName{txIdLabel},
				})),
				metricsProvider: metricsProvider,
				metrics:         newMetrics(metricsProvider),
				checkService:    checkService,
				lockConfig:      lockConfig,
			}

			return auditor, nil
		}),
		networkProvider:     networkProvider,
		tokenServiceManager: tokensServiceManager,
		tmsProvider:         tmsProvider,
		configService:       configService,
	}
}

// Auditor returns the Service for the given wallet
func (cm *ServiceManager) Auditor(tmsID token.TMSID) (*Service, error) {
	return cm.p.Get(tmsID)
}

var managerType = reflect.TypeFor[*ServiceManager]()

// Get returns the Service instance for the passed auditor wallet
func Get(sp token.ServiceProvider, w *token.AuditorWallet) *Service {
	if w == nil {
		logger.Debugf("no wallet provided")

		return nil
	}

	return GetByTMSID(sp, w.TMS().ID())
}

// GetByTMSID returns the Service instance for the passed auditor wallet
func GetByTMSID(sp token.ServiceProvider, tmsID token.TMSID) *Service {
	s, err := sp.GetService(managerType)
	if err != nil {
		logger.Errorf("failed to get manager service: [%s]", err)

		return nil
	}
	auditor, err := s.(*ServiceManager).Auditor(tmsID)
	if err != nil {
		logger.Errorf("failed to get db for tms [%s]: [%s]", tmsID, err)

		return nil
	}

	return auditor
}

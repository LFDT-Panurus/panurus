/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cleanup

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	dbdriver "github.com/LFDT-Panurus/panurus/token/services/storage/db/driver"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services"
	"github.com/LFDT-Panurus/panurus/token/services/tokens"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

type ServiceManager services.ServiceManager[*Manager]

type Configuration interface {
	// ConfigurationFor returns the configuration for the given coordinates
	ConfigurationFor(network, channel, namespace string) (*config.Configuration, error)
}

func NewServiceManager(
	configuration Configuration,
	identityStorageProvider identity.StorageProvider,
	tokensProvider *tokens.ServiceManager,
) ServiceManager {
	return services.NewServiceManager(func(tmsID token.TMSID) (*Manager, error) {
		// Get TMS configuration
		cfg, err := configuration.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
		if err != nil {
			logger.Warnf("failed to get configuration for [%s], using default cleanup config: %v", tmsID, err)
			cfg = nil
		}

		// Load cleanup configuration
		var cleanupConfig Config
		if cfg != nil {
			cleanupConfig, err = LoadConfig(cfg)
			if err != nil {
				logger.Warnf("failed to load cleanup config for [%s], using defaults: %v", tmsID, err)
				cleanupConfig = DefaultConfig()
			}
		} else {
			cleanupConfig = DefaultConfig()
		}

		// Create keystore provider that wraps the TMS provider
		keystoreProvider := &tmsKeystoreProvider{
			identityStorageProvider: identityStorageProvider,
		}

		// Wrap the token storage to adapt the leadership interface
		tokensService, err := tokensProvider.ServiceByTMSId(tmsID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get tokens service for [%s]", tmsID)
		}

		storageAdapter := &cleanupStorageAdapter{storage: tokensService.Storage.TokenDB}

		identityStore, err := identityStorageProvider.IdentityStore(tmsID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get identity store for [%s]", tmsID)
		}
		extractor := NewSKIExtractor()
		extractor.RegisterProvider(idemix.IdentityTypeString, idemix.NewSKIProvider())
		extractor.RegisterProvider(idemixnym.IdentityTypeString, idemixnym.NewSKIProvider(identityStore))
		extractor.RegisterProvider(x509.IdentityTypeString, NewNoopSKIProvider())

		manager := NewManager(
			logger,
			storageAdapter,
			extractor,
			keystoreProvider,
			tmsID,
			cleanupConfig,
		)

		// Start the cleanup manager
		if err := manager.Start(); err != nil {
			return nil, errors.Wrapf(err, "failed to start cleanup manager for [%s]", tmsID)
		}

		logger.Debugf("cleanup manager started for namespace [%s]", tmsID.Namespace)

		return manager, nil
	})
}

// tmsKeystoreProvider adapts the TMS provider to provide keystore access
type tmsKeystoreProvider struct {
	identityStorageProvider identity.StorageProvider
}

func (p *tmsKeystoreProvider) Keystore(tmsID token.TMSID) (Keystore, error) {
	return p.identityStorageProvider.Keystore(tmsID)
}

type cleanupStorage interface {
	AcquireCleanupLeadership(ctx context.Context, lockID int64) (dbdriver.CleanupLeadership, bool, error)
	GetDeletedTokensPendingSKICleanup(ctx context.Context, olderThan time.Duration, limit int) ([]dbdriver.DeletedToken, error)
	MarkTokenCleaned(ctx context.Context, txID string, index uint64, cleanedBy string) error
}

// cleanupStorageAdapter adapts the tokendb storage to the cleanup.Storage interface
type cleanupStorageAdapter struct {
	storage cleanupStorage
}

func (a *cleanupStorageAdapter) AcquireCleanupLeadership(ctx context.Context, lockID int64) (Leadership, bool, error) {
	leadership, acquired, err := a.storage.AcquireCleanupLeadership(ctx, lockID)
	if err != nil || !acquired {
		return nil, acquired, err
	}

	return &leadershipAdapter{leadership: leadership}, true, nil
}

func (a *cleanupStorageAdapter) GetDeletedTokensPendingSKICleanup(ctx context.Context, olderThan time.Duration, limit int) ([]DeletedToken, error) {
	driverTokens, err := a.storage.GetDeletedTokensPendingSKICleanup(ctx, olderThan, limit)
	if err != nil {
		return nil, err
	}
	// Convert driver.DeletedToken to DeletedToken
	tokens := make([]DeletedToken, len(driverTokens))
	for i, dt := range driverTokens {
		tokens[i] = DeletedToken{
			TxID:          dt.TxID,
			Index:         dt.Index,
			OwnerIdentity: dt.OwnerIdentity,
			OwnerType:     dt.OwnerType,
			DeletedAt:     dt.DeletedAt,
		}
	}

	return tokens, nil
}

func (a *cleanupStorageAdapter) MarkTokenCleaned(ctx context.Context, txID string, index uint64, cleanedBy string) error {
	return a.storage.MarkTokenCleaned(ctx, txID, index, cleanedBy)
}

// leadershipAdapter adapts driver.CleanupLeadership to Leadership
type leadershipAdapter struct {
	leadership dbdriver.CleanupLeadership
}

func (l *leadershipAdapter) Close() error {
	return l.leadership.Close()
}

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tokens

import (
	"reflect"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services"
	"github.com/LFDT-Panurus/panurus/token/services/network"
	"github.com/LFDT-Panurus/panurus/token/services/storage/tokendb"
	"github.com/LFDT-Panurus/panurus/token/services/utils/cache"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/events"
)

var managerType = reflect.TypeFor[*ServiceManager]()

// StoreServiceManager defines the interface for obtaining a token database store service by TMS ID.
type StoreServiceManager = tokendb.StoreServiceManager

// TMSProvider defines the interface for obtaining a token management service.
type TMSProvider interface {
	// GetManagementService returns the management service for the given options.
	GetManagementService(opts ...token.ServiceOption) (*token.ManagementService, error)
}

// NetworkProvider defines the interface for obtaining a network instance.
type NetworkProvider interface {
	// GetNetwork returns the network for the given network and channel identifiers.
	GetNetwork(network string, channel string) (*network.Network, error)
}

// ServiceManager handles the lifecycle and lazy initialization of Service instances per TMS.
// It uses a lazy provider to ensure that services are only created when needed.
type ServiceManager struct {
	p lazy.Provider[token.TMSID, *Service]
}

// NewServiceManager creates a new ServiceManager instance.
func NewServiceManager(
	tmsProvider TMSProvider,
	storeServiceManager StoreServiceManager,
	networkProvider NetworkProvider,
	notifier events.Publisher,
) *ServiceManager {
	return &ServiceManager{
		p: lazy.NewProviderWithKeyMapper(services.Key, func(tmsID token.TMSID) (*Service, error) {
			db, err := storeServiceManager.StoreServiceByTMSId(tmsID)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get tokendb for [%s]", tmsID)
			}

			storage, err := NewDBStorage(notifier, db, tmsID)
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get token store for [%s]", tmsID)
			}
			cacheInst, err := cache.NewDefaultRistrettoCache[*CacheEntry]()
			if err != nil {
				return nil, errors.WithMessagef(err, "failed to get token cache for [%s]", tmsID)
			}
			tokens := NewService(tmsID, tmsProvider, networkProvider, storage, cacheInst)

			return tokens, nil
		}),
	}
}

// ServiceByTMSId returns the Service instance associated with the given TMS identifier.
func (cm *ServiceManager) ServiceByTMSId(tmsID token.TMSID) (*Service, error) {
	return cm.p.Get(tmsID)
}

// GetService is a helper function that retrieves the ServiceManager from the provider and returns the Service for the given TMS ID.
func GetService(sp token.ServiceProvider, tmsID token.TMSID) (*Service, error) {
	s, err := sp.GetService(managerType)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get manager service")
	}
	if err != nil {
		return nil, err
	}
	tokens, err := s.(*ServiceManager).ServiceByTMSId(tmsID)
	if err != nil {
		return nil, err
	}

	return tokens, nil
}

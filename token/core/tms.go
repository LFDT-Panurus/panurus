/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"runtime/debug"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var logger = logging.MustGetLogger()

// CallbackFunc defines a function signature for a callback invoked after a new TMS is created.
type CallbackFunc func(tms driver.TokenManagerService, network, channel, namespace string) error

// PublicParametersStorage defines the interface for retrieving public parameters from a storage.
//
//go:generate counterfeiter -o mock/pps.go -fake-name PublicParametersStorage . PublicParametersStorage
type PublicParametersStorage interface {
	// PublicParams returns the public parameters for the specified network, channel, and namespace.
	PublicParams(ctx context.Context, networkID string, channel string, namespace string) ([]byte, error)
}

// ConfigService defines the interface for retrieving TMS configurations.
//
//go:generate counterfeiter -o mock/cs.go -fake-name ConfigService . ConfigService
type ConfigService interface {
	// Configurations returns all available TMS configurations.
	Configurations() ([]driver.Configuration, error)
	// ConfigurationFor returns the TMS configuration for the specified network, channel, and namespace.
	ConfigurationFor(network string, channel string, namespace string) (driver.Configuration, error)
}

// PublicParameters represents the configuration for public parameters path.
type PublicParameters struct {
	// Path is the filesystem path to the public parameters.
	Path string `yaml:"path"`
}

// TMSProvider is a token management service provider.
// It is responsible for creating and caching token management services for different networks.
type TMSProvider struct {
	configService           ConfigService
	publicParametersStorage PublicParametersStorage
	callback                CallbackFunc
	tokenDriverService      *TokenDriverService

	lock     sync.RWMutex
	services map[string]driver.TokenManagerService
}

func NewTMSProvider(
	configService ConfigService,
	pps PublicParametersStorage,
	tokenDriverService *TokenDriverService,
) *TMSProvider {
	ms := &TMSProvider{
		configService:           configService,
		publicParametersStorage: pps,
		services:                map[string]driver.TokenManagerService{},
		tokenDriverService:      tokenDriverService,
	}

	return ms
}

// GetTokenManagerService returns a driver.TokenManagerService instance for the passed parameters.
// If a TokenManagerService is not available, it creates one by first fetching the public parameters using the passed driver.PublicParamsFetcher.
// If no driver is registered for the public params identifier, it returns an error.
func (m *TMSProvider) GetTokenManagerService(opts driver.ServiceOptions) (service driver.TokenManagerService, err error) {
	if len(opts.Network) == 0 {
		return nil, errors.Errorf("network not specified")
	}
	if len(opts.Namespace) == 0 {
		return nil, errors.Errorf("namespace not specified")
	}

	key := tmsKey(opts)
	logger.Debugf("check existence token manager service for [%s] with key [%s]", opts, key)
	m.lock.RLock()
	service, ok := m.services[key]
	if ok {
		m.lock.RUnlock()

		return service, nil
	}
	m.lock.RUnlock()

	logger.Debugf("lock to create token manager service for [%s] with key [%s]", opts, key)

	m.lock.Lock()
	defer m.lock.Unlock()

	service, ok = m.services[key]
	if ok {
		logger.Debugf("token manager service for [%s] with key [%s] exists, return it", opts, key)

		return service, nil
	}

	logger.Debugf("creating new token manager service for [%s] with key [%s]", opts, key)
	service, err = m.getTokenManagerService(opts)
	if err != nil {
		return nil, err
	}
	m.services[key] = service

	return service, nil
}

// NewTokenManagerService creates a new driver.TokenManagerService instance for the passed parameters.
// It does not cache the created service.
func (m *TMSProvider) NewTokenManagerService(opts driver.ServiceOptions) (driver.TokenManagerService, error) {
	if len(opts.Network) == 0 {
		return nil, errors.Errorf("network not specified")
	}
	if len(opts.Namespace) == 0 {
		return nil, errors.Errorf("namespace not specified")
	}
	logger.Debugf("creating new token manager service for [%s]", opts)

	service, err := m.newTMS(&opts)
	if err != nil {
		return nil, err
	}

	return service, nil
}

// Update updates the public parameters for the specified TMS.
// If the service is already cached and the public parameters are different, it unloads the old service and reloads it.
func (m *TMSProvider) Update(opts driver.ServiceOptions) (err error) {
	if len(opts.Network) == 0 {
		return errors.Errorf("network not specified")
	}
	if len(opts.Namespace) == 0 {
		return errors.Errorf("namespace not specified")
	}
	if len(opts.PublicParams) == 0 {
		return errors.Errorf("public params not specified")
	}

	key := tmsKey(opts)
	logger.Debugf("update tms for [%s] with key [%s]", opts, key)

	m.lock.Lock()
	defer m.lock.Unlock()
	service, ok := m.services[key]
	if !ok {
		logger.Debugf("no service found, instantiate token management system for [%s:%s:%s] for key [%s]", opts.Network, opts.Channel, opts.Namespace, key)
	} else {
		// update only if the public params are different from the current
		digest := sha256.Sum256(opts.PublicParams)
		if bytes.Equal(service.PublicParamsManager().PublicParamsHash(), digest[:]) {
			logger.Debugf("service found, no need to update token management system for [%s:%s:%s] for key [%s], public params are the same", opts.Network, opts.Channel, opts.Namespace, key)

			return nil
		}

		logger.Debugf("service found, unload token management system for [%s:%s:%s] for key [%s] and reload it", opts.Network, opts.Channel, opts.Namespace, key)
	}

	// create the service for the new public params
	newService, err := m.getTokenManagerService(opts)
	if err == nil {
		// unload the old service, if set
		if service != nil {
			if err := service.Done(); err != nil {
				return errors.WithMessagef(err, "failed to unload token service")
			}
		}
		// register the new service
		m.services[key] = newService
	}

	return err
}

// SetCallback sets the callback function to be invoked when a new TMS is created.
func (m *TMSProvider) SetCallback(callback CallbackFunc) {
	m.callback = callback
}

func (m *TMSProvider) getTokenManagerService(opts driver.ServiceOptions) (service driver.TokenManagerService, err error) {
	logger.Debugf("creating new token manager service for [%s]", opts)
	service, err = m.newTMS(&opts)
	if err != nil {
		return nil, err
	}
	// invoke callback
	if m.callback != nil {
		err = m.callback(service, opts.Network, opts.Channel, opts.Namespace)
		if err != nil {
			logger.Fatalf("failed to initialize tms for [%s]: [%s]", opts, err)
		}
	}

	return service, nil
}

func (m *TMSProvider) newTMS(opts *driver.ServiceOptions) (driver.TokenManagerService, error) {
	ppRaw, err := m.loadPublicParams(opts)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get driver for [%s]", opts)
	}
	opts.PublicParams = ppRaw
	logger.Debugf("instantiating token service for [%s]", opts)

	ts, err := m.tokenDriverService.NewTokenService(driver.TMSID{Network: opts.Network, Channel: opts.Channel, Namespace: opts.Namespace}, opts.PublicParams)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to instantiate token service for [%s]", opts)
	}

	return ts, nil
}

func (m *TMSProvider) loadPublicParams(opts *driver.ServiceOptions) ([]byte, error) {
	// priorities:
	// 1. opts.PublicParams
	// 2. publicParametersStorage
	// 3. local configuration
	// 4. public parameters fetcher, if any
	for _, retriever := range []func(options *driver.ServiceOptions) ([]byte, error){m.ppFromOpts, m.ppFromStorage, m.ppFromConfig, m.ppFromFetcher} {
		if ppRaw, err := retriever(opts); err != nil {
			logger.Warnf("failed to retrieve params for [%s]: [%s]", opts, err)
		} else if len(ppRaw) != 0 {
			return ppRaw, nil
		}
	}
	logger.Errorf("cannot retrieve public params for [%s]: [%s]", opts, string(debug.Stack()))

	return nil, errors.Errorf("cannot retrieve public params for [%s]", opts)
}

func (m *TMSProvider) ppFromOpts(opts *driver.ServiceOptions) ([]byte, error) {
	if len(opts.PublicParams) != 0 {
		return opts.PublicParams, nil
	}

	return nil, errors.Errorf("public parameter not found in options")
}

func (m *TMSProvider) ppFromStorage(opts *driver.ServiceOptions) ([]byte, error) {
	ppRaw, err := m.publicParametersStorage.PublicParams(context.Background(), opts.Network, opts.Channel, opts.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load public params from the publicParametersStorage")
	}
	if len(ppRaw) == 0 {
		return nil, errors.Errorf("no public parames found in publicParametersStorage")
	}

	return ppRaw, nil
}

func (m *TMSProvider) ppFromConfig(opts *driver.ServiceOptions) ([]byte, error) {
	tmsConfig, err := m.configService.ConfigurationFor(opts.Network, opts.Channel, opts.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to identify driver from the configuration of [%s], loading driver from public parameters failed too [%s]", opts, err)
	}
	cPP := &PublicParameters{}
	if err := tmsConfig.UnmarshalKey("publicParameters", cPP); err != nil {
		return nil, errors.WithMessagef(err, "failed to unmarshal public parameters")
	}
	if len(cPP.Path) != 0 {
		logger.Infof("load public parameters from [%s]...", cPP.Path)
		ppRaw, err := os.ReadFile(cPP.Path)
		if err != nil {
			return nil, errors.Errorf("failed to load public parameters from [%s]: [%s]", cPP.Path, err)
		}

		return ppRaw, nil
	}

	return nil, errors.Errorf("no public params found in configuration")
}

func (m *TMSProvider) ppFromFetcher(opts *driver.ServiceOptions) ([]byte, error) {
	if opts.PublicParamsFetcher != nil {
		ppRaw, err := opts.PublicParamsFetcher.Fetch()
		if err != nil {
			return nil, errors.WithMessagef(err, "failed fetching public parameters")
		}
		if len(ppRaw) == 0 {
			return nil, errors.Errorf("no public parames found in publicParametersStorage")
		}
		opts.PublicParams = ppRaw

		return ppRaw, nil
	}

	return nil, errors.Errorf("no public params fetched available")
}

func tmsKey(opts driver.ServiceOptions) string {
	return opts.Network + opts.Channel + opts.Namespace
}

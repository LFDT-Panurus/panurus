/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"context"
	"sync"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

var (
	managementServiceProviderIndex = &ManagementServiceProvider{}
)

// Normalizer is used to set default values of ServiceOptions struct, if needed.
type Normalizer interface {
	// Normalize normalizes the given ServiceOptions struct.
	Normalize(opt *ServiceOptions) (*ServiceOptions, error)
}

type TMSNormalizer Normalizer

//go:generate counterfeiter -o mock/vp.go -fake-name VaultProvider . VaultProvider

// VaultProvider provides token vault instances
type VaultProvider interface {
	// Vault returns a token vault instance for the passed inputs
	Vault(network string, channel string, namespace string) (driver.Vault, error)
}

// SelectorManager handles token selection operations
type SelectorManager interface {
	// NewSelector returns a new Selector instance bound the passed id.
	NewSelector(id string) (Selector, error)
	// Unlock unlocks the tokens bound to the passed id, if any
	Unlock(ctx context.Context, id string) error
	// Close closes the selector and releases its memory/cpu resources
	Close(id string) error
}

// SelectorManagerProvider provides instances of SelectorManager
type SelectorManagerProvider interface {
	// SelectorManager returns a SelectorManager instance for the passed inputs.
	SelectorManager(tms *ManagementService) (SelectorManager, error)
}

// Shutdownable is optionally implemented by SelectorManagerProvider instances
// that manage background goroutines and need to be stopped when a TMS is evicted.
type Shutdownable interface {
	Shutdown()
}

// CertificationClientProvider provides instances of CertificationClient
type CertificationClientProvider interface {
	// New returns a new CertificationClient instance for the passed inputs
	New(ctx context.Context, tms *ManagementService) (driver.CertificationClient, error)
}

// ManagementServiceProvider provides instances of the management service
type ManagementServiceProvider struct {
	logger                      logging.Logger
	tmsProvider                 driver.TokenManagerServiceProvider
	normalizer                  TMSNormalizer
	certificationClientProvider CertificationClientProvider
	selectorManagerProvider     SelectorManagerProvider
	vaultProvider               VaultProvider

	lock     sync.RWMutex
	services map[string]*ManagementService
}

// NewManagementServiceProvider returns a new instance of ManagementServiceProvider
func NewManagementServiceProvider(
	tmsProvider driver.TokenManagerServiceProvider,
	normalizer TMSNormalizer,
	vaultProvider VaultProvider,
	certificationClientProvider CertificationClientProvider,
	selectorManagerProvider SelectorManagerProvider,
) *ManagementServiceProvider {
	return &ManagementServiceProvider{
		logger:                      logger,
		tmsProvider:                 tmsProvider,
		normalizer:                  normalizer,
		certificationClientProvider: certificationClientProvider,
		selectorManagerProvider:     selectorManagerProvider,
		vaultProvider:               vaultProvider,
		services:                    map[string]*ManagementService{},
	}
}

// GetManagementServiceProvider returns the management service provider from the passed service provider.
// The function panics if an error occurs.
// An alternative way is to use `s, err := sp.GetService(&ManagementServiceProvider{}) and catch the error manually.`
func GetManagementServiceProvider(sp ServiceProvider) *ManagementServiceProvider {
	s, err := sp.GetService(managementServiceProviderIndex)
	if err != nil {
		panic(err)
	}

	return s.(*ManagementServiceProvider)
}

// GetManagementService returns an instance of the management service for the passed options.
// If the management service has not been created yet, it will be created.
func (p *ManagementServiceProvider) GetManagementService(opts ...ServiceOption) (*ManagementService, error) {
	return p.managementService(opts...)
}

// Update uses the given options to update the public parameters of a given TMS.
// If the public parameters in the options are identical to those in the current TMS, then nothing happens.
// If a TMS does not exist for the given options, one is created with the given public parameters.
func (p *ManagementServiceProvider) Update(tmsID TMSID, val []byte) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	key := tmsID.Network + tmsID.Channel + tmsID.Namespace
	opts := driver.ServiceOptions{
		Network:      tmsID.Network,
		Channel:      tmsID.Channel,
		Namespace:    tmsID.Namespace,
		PublicParams: val,
	}
	p.logger.Infof("update tms [%s] with public params [%s]", tmsID, Hashable(val))
	if err := p.tmsProvider.Update(opts); err != nil {
		return errors.Wrapf(err, "failed updating tms [%s]", tmsID)
	}

	// The core provider keeps its service when the parameters did not change. In that case the
	// cached service still wraps the live one and there is nothing to reload. Going on would
	// still shut down every selector manager below, for every TMS on this node, on a call that
	// changed nothing; callers that re-apply what they observe (the EVM driver's watcher does on
	// startup and on every retry) would then churn the selectors for no reason.
	if s, ok := p.services[key]; ok && s.tms != nil {
		current, err := p.tmsProvider.GetTokenManagerService(opts)
		if err == nil && current == s.tms {
			p.logger.Debugf("tms [%s] already runs public params [%s], nothing to reload", tmsID, Hashable(val))

			return nil
		}
	}

	// Shut down background goroutines for the evicted TMS before clearing the cache.
	if s, ok := p.selectorManagerProvider.(Shutdownable); ok {
		s.Shutdown()
	}

	// clear cache
	delete(p.services, key)

	p.logger.Infof("update tms [%s] with public params [%s]...done", tmsID, Hashable(val))

	return nil
}

// ConfigurationFor returns the configuration for the given TMS id without building a
// management service. It returns an error if no configuration is found.
func (p *ManagementServiceProvider) ConfigurationFor(tmsID TMSID) (driver.Configuration, error) {
	return p.tmsProvider.ConfigurationFor(tmsID.Network, tmsID.Channel, tmsID.Namespace)
}

func (p *ManagementServiceProvider) managementService(opts ...ServiceOption) (*ManagementService, error) {
	opt, err := CompileServiceOptions(opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to compile options")
	}
	opt, err = p.normalizer.Normalize(opt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to normalize options")
	}

	key := opt.Network + opt.Channel + opt.Namespace
	p.logger.Debugf("check existence token manager service for [%s]", key)
	p.lock.RLock()
	service, ok := p.services[key]
	p.lock.RUnlock()
	if ok {
		return service, nil
	}

	p.logger.Debugf("lock to create token manager service for [%s]", key)

	p.lock.Lock()
	defer p.lock.Unlock()

	var tokenService driver.TokenManagerService
	driverOpts := driver.ServiceOptions{
		Network:             opt.Network,
		Channel:             opt.Channel,
		Namespace:           opt.Namespace,
		PublicParamsFetcher: opt.PublicParamsFetcher,
		PublicParams:        opt.PublicParams,
		Params:              opt.Params,
	}
	tokenService, err = p.tmsProvider.GetTokenManagerService(driverOpts)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting TMS for [%s]", opt)
	}

	p.logger.Debugf("returning tms for [%s,%s,%s]", opt.Network, opt.Channel, opt.Namespace)

	ms, err := NewManagementService(
		TMSID{
			Network:   opt.Network,
			Channel:   opt.Channel,
			Namespace: opt.Namespace,
		},
		tokenService,
		logging.DeriveDriverLogger(p.logger, "", opt.Network, opt.Channel, opt.Namespace),
		p.vaultProvider,
		p.certificationClientProvider,
		p.selectorManagerProvider,
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to initialize token management service")
	}
	p.services[key] = ms

	return ms, nil
}

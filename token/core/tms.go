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
	service, err = m.getTokenManagerService(opts, m.publicParamsSources())
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

	service, err := m.newTMS(&opts, m.publicParamsSources())
	if err != nil {
		return nil, err
	}

	return service, nil
}

// Update updates the public parameters for the specified TMS.
// If the service is already cached and the public parameters are different, it unloads the old service and reloads it.
// The TMS is rebuilt from the public parameters carried by the update and from nothing else: if the
// driver rejects them, Update fails and the currently cached service is left untouched.
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

	// create the service for the new public params. Only the public params carried by the update
	// are eligible: see updatePublicParamsSources.
	newService, err := m.getTokenManagerService(opts, m.updatePublicParamsSources())
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

// ConfigurationFor returns the configuration for the given TMS coordinates without
// instantiating the token manager service. It returns an error if no configuration
// is registered for the coordinates.
func (m *TMSProvider) ConfigurationFor(network, channel, namespace string) (driver.Configuration, error) {
	return m.configService.ConfigurationFor(network, channel, namespace)
}

func (m *TMSProvider) getTokenManagerService(opts driver.ServiceOptions, sources []ppSource) (service driver.TokenManagerService, err error) {
	logger.Debugf("creating new token manager service for [%s]", opts)
	service, err = m.newTMS(&opts, sources)
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

// ppSource is a named source of public parameters. newTMS walks the sources in priority
// order, giving each one the chance to produce public parameters the driver accepts.
type ppSource struct {
	// name identifies the source in logs and in the aggregated error.
	name string
	// retrieve returns the raw public parameters held by this source.
	retrieve func(opts *driver.ServiceOptions) ([]byte, error)
}

// publicParamsSources returns the public parameters sources in priority order:
//  1. opts.PublicParams, the caller-provided public parameters;
//  2. the local public parameters storage;
//  3. the local configuration (publicParameters.path);
//  4. the public parameters fetcher, if any. This is the authoritative source: it reads
//     the public parameters from the network.
func (m *TMSProvider) publicParamsSources() []ppSource {
	return []ppSource{
		{name: "options", retrieve: m.ppFromOpts},
		{name: "storage", retrieve: m.ppFromStorage},
		{name: "configuration", retrieve: m.ppFromConfig},
		{name: "fetcher", retrieve: m.ppFromFetcher},
	}
}

// updatePublicParamsSources returns the only public parameters source an update may resolve
// from: the public parameters the update itself carries.
//
// Update means "adopt exactly these public parameters". Letting it fall back to another source
// would make it report success while the TMS keeps running on a different - necessarily older -
// copy, after having already unloaded the previous service. On the update path the fallback
// cannot even reach the ledger, because the options an update is built from carry no
// PublicParamsFetcher, so it could only ever land on a stale local copy: the opposite of what
// the walk is for. A rejected update must therefore fail, and loudly.
func (m *TMSProvider) updatePublicParamsSources() []ppSource {
	return []ppSource{
		{name: "options", retrieve: m.ppFromOpts},
	}
}

// newTMS instantiates the token manager service identified by the passed options, resolving its
// public parameters from the passed sources.
//
// It walks the sources in the given order and, for each of them, retrieves the public parameters
// and immediately tries to instantiate the token service with them. The first source whose
// public parameters the driver accepts wins. A source that yields no public parameters, that
// fails to yield them, or that yields public parameters the driver rejects (driver name/version
// skew, unparsable container, invalid field, and so on) does not stop the walk: the next source
// gets its chance. This way a stale or malformed local copy does not shadow the authoritative
// public parameters fetched from the network.
//
// Callers that resolve a TMS pass publicParamsSources. Update passes
// updatePublicParamsSources, because adopting a different copy than the one it carries would be
// a silent failure rather than a recovery.
//
// On failure, the returned error aggregates the failure of every source, so that the offending
// source can be identified. It wraps ErrTMSNotFound if, and only if, no source produced any
// public parameters at all, which signals that the TMS has not been set up yet.
func (m *TMSProvider) newTMS(opts *driver.ServiceOptions, sources []ppSource) (driver.TokenManagerService, error) {
	tmsID := driver.TMSID{Network: opts.Network, Channel: opts.Channel, Namespace: opts.Namespace}
	// public parameters already tried, indexed by digest, to not pay the driver deserialization
	// cost twice when two sources hold the very same public parameters.
	tried := make(map[[sha256.Size]byte]string, len(sources))
	// produced counts the sources that held public parameters, duplicates included. It is what
	// separates "the TMS is not set up yet" from "the TMS is set up with public parameters this
	// node cannot use", and it is counted rather than inferred from the errors collected below.
	produced := 0
	var retrievalErrs, instantiationErrs []error

	for _, source := range sources {
		ppRaw, err := source.retrieve(opts)
		switch {
		case err != nil:
			logger.Warnf("failed to retrieve public params for [%s] from [%s]: [%s]", opts, source.name, err)
			retrievalErrs = append(retrievalErrs, errors.WithMessagef(err, "cannot retrieve public params from [%s]", source.name))

			continue
		// defensive: every source above reports emptiness as an error, so this branch guards
		// the ppSource contract for sources added later rather than a reachable state today.
		case len(ppRaw) == 0:
			logger.Debugf("no public params for [%s] from [%s]", opts, source.name)
			retrievalErrs = append(retrievalErrs, errors.Errorf("cannot retrieve public params from [%s]: no public params returned", source.name))

			continue
		}

		produced++

		digest := sha256.Sum256(ppRaw)
		if previous, ok := tried[digest]; ok {
			logger.Debugf("public params for [%s] from [%s] are identical to those from [%s], skip them", opts, source.name, previous)
			// record the skip: this source was walked, and the aggregated error must say so.
			instantiationErrs = append(instantiationErrs, errors.Errorf("public params from [%s] are identical to those from [%s], already tried", source.name, previous))

			continue
		}
		tried[digest] = source.name

		logger.Debugf("instantiating token service for [%s] with the public params from [%s]", opts, source.name)
		ts, err := m.newTokenService(tmsID, ppRaw)
		if err != nil {
			logger.Warnf("failed to instantiate token service for [%s] with the public params from [%s], try next source: [%s]", opts, source.name, err)
			instantiationErrs = append(instantiationErrs, errors.WithMessagef(err, "cannot instantiate token service with the public params from [%s]", source.name))

			continue
		}

		if len(instantiationErrs) != 0 {
			logger.Infof("instantiated token service for [%s] with the public params from [%s] after [%d] unusable public params", opts, source.name, len(instantiationErrs))
		}

		return ts, nil
	}

	if produced == 0 {
		// no source produced any public parameters: the TMS does not exist, yet.
		logger.Errorf("cannot retrieve public params for [%s]: [%s]", opts, string(debug.Stack()))

		return nil, errors.Join(append([]error{errors.Errorf("cannot retrieve public params for [%s]", opts), ErrTMSNotFound}, retrievalErrs...)...)
	}

	// at least one source produced public parameters, but none of them was usable.
	errs := append([]error{errors.Errorf("failed to instantiate token service for [%s]", opts)}, instantiationErrs...)

	return nil, errors.Join(append(errs, retrievalErrs...)...)
}

// newTokenService instantiates the token service for the passed public parameters, converting
// a panic raised while deserializing them into an error. The public parameters are attacker
// controlled (they come from the network) or simply stale (they come from a local copy written
// by an older version), and a driver that panics on them must not take down the resolution of
// the remaining public parameters sources with it.
func (m *TMSProvider) newTokenService(tmsID driver.TMSID, ppRaw []byte) (ts driver.TokenManagerService, err error) {
	defer func() {
		if r := recover(); r != nil {
			// the walk swallows this error and moves on, so the stack is logged here or it is
			// lost: nothing downstream can say where a driver panicked.
			logger.Errorf("panic while instantiating token service for [%s] with public params of [%d] bytes: [%v]\n%s", tmsID, len(ppRaw), r, string(debug.Stack()))
			ts, err = nil, errors.Errorf("caught panic while instantiating token service: [%v]", r)
		}
	}()

	return m.tokenDriverService.NewTokenService(tmsID, ppRaw)
}

func (m *TMSProvider) ppFromOpts(opts *driver.ServiceOptions) ([]byte, error) {
	if len(opts.PublicParams) != 0 {
		return opts.PublicParams, nil
	}

	return nil, errors.Errorf("public parameter not found in options")
}

func (m *TMSProvider) ppFromStorage(opts *driver.ServiceOptions) ([]byte, error) {
	if m.publicParametersStorage == nil {
		return nil, errors.Errorf("no publicParametersStorage available")
	}
	ppRaw, err := m.publicParametersStorage.PublicParams(context.Background(), opts.Network, opts.Channel, opts.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load public params from the publicParametersStorage")
	}
	if len(ppRaw) == 0 {
		return nil, errors.Errorf("no public params found in publicParametersStorage")
	}

	return ppRaw, nil
}

func (m *TMSProvider) ppFromConfig(opts *driver.ServiceOptions) ([]byte, error) {
	tmsConfig, err := m.configService.ConfigurationFor(opts.Network, opts.Channel, opts.Namespace)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to load the configuration of [%s]", opts)
	}
	if tmsConfig == nil {
		return nil, errors.Errorf("no configuration found for [%s]", opts)
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
			return nil, errors.Errorf("no public params fetched")
		}

		return ppRaw, nil
	}

	return nil, errors.Errorf("no PublicParamsFetcher configured")
}

func tmsKey(opts driver.ServiceOptions) string {
	return opts.Network + opts.Channel + opts.Namespace
}

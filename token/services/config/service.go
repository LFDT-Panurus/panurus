/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/collections"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/config"
)

var logger = logging.MustGetLogger()

const (
	RootKey = "token"
	TMSKey  = "tms"
)

var (
	TMSPath     = config.Join(RootKey, TMSKey)
	VersionPath = config.Join(RootKey, "version")
	EnabledPath = config.Join(RootKey, "enabled")

	// ErrConfigurationNotFound is returned when a configuration is not found
	ErrConfigurationNotFound = errors.Errorf("configuration not found")
)

type Provider interface {
	UnmarshalKey(key string, rawVal any) error
	GetString(key string) string
	IsSet(key string) bool
	TranslatePath(path string) string
	GetBool(s string) bool
	MergeConfig(raw []byte) error
	ProvideFromRaw(raw []byte) (*config.Provider, error)
}

// Service model the configuration service for Panurus
type Service struct {
	cp Provider

	version              string
	enabled              bool
	configurationsHolder lazy.Holder[map[string]*Configuration]
	validators           []Validator
}

// NewService creates a new Service configuration.
func NewService(cp Provider) *Service {
	version := cp.GetString(VersionPath)
	if len(version) == 0 {
		version = "v1"
	}
	enabled := cp.GetBool(EnabledPath)
	loader := &loader{cp: cp, validators: nil}

	return &Service{
		cp:                   cp,
		version:              version,
		enabled:              enabled,
		configurationsHolder: lazy.NewHolder(loader.load, loader.close),
		validators:           []Validator{},
	}
}

func (m *Service) Version() string {
	return m.version
}

func (m *Service) Enabled() bool {
	return m.enabled
}

// LookupNamespace searches for a configuration that matches the given network and channel, and
// return its namespace.
// If no matching configuration is found, an error is returned.
// If multiple matching configurations are found, an error is returned.
func (m *Service) LookupNamespace(network, channel string) (string, error) {
	tmsConfigs, err := m.configurations()
	if err != nil {
		return "", err
	}

	var hits []driver.TMSID
	for _, config := range tmsConfigs {
		id := config.ID()
		if id.Network == network && id.Channel == channel {
			hits = append(hits, id)
		}
	}
	if len(hits) == 1 {
		return hits[0].Namespace, nil
	}
	if len(hits) == 0 {
		return "", errors.Errorf("no configuration for network [%s], channel [%s]", network, channel)
	}

	return "", errors.Errorf("multiple configurations for network [%s], channel [%s]", network, channel)
}

// ConfigurationFor returns a configuration for the given network, channel, and namespace.
func (m *Service) ConfigurationFor(network, channel, namespace string) (*Configuration, error) {
	tmsConfigs, err := m.configurations()
	if err != nil {
		return nil, err
	}

	for _, config := range tmsConfigs {
		id := config.ID()
		if id.Network == network && id.Channel == channel && id.Namespace == namespace {
			return config, nil
		}
	}

	return nil, errors.Wrapf(ErrConfigurationNotFound, "no configuration for network [%s], channel [%s], namespace [%s]", network, channel, namespace)
}

// Configurations returns all configuration configurations.
func (m *Service) Configurations() ([]*Configuration, error) {
	tmsConfigs, err := m.configurations()
	if err != nil {
		return nil, errors.Wrapf(err, "failed loading configurations")
	}

	return collections.Values(tmsConfigs), nil
}

// AddConfiguration does the following:
// - parse raw as a yaml stream
// - extract the configuration
// - validate it making sure it contains a new TMS
// If all good, accept the new TMS
// Updates to an existing TMS should be rejected.
func (m *Service) AddConfiguration(raw []byte) error {
	logger.Infof("Add configuration from raw [%d bytes]", len(raw))
	// Do the following:
	// - parse raw as a yaml stream
	v, err := m.cp.ProvideFromRaw(raw)
	if err != nil {
		return errors.Wrapf(err, "failed loading configuration")
	}

	return m.addConfiguration(v, raw)
}

func (m *Service) addConfiguration(cp Provider, raw []byte) error {
	// - extract the configuration
	loader := &loader{cp: cp}
	configurations, err := loader.load()
	if err != nil {
		return errors.Wrapf(err, "failed loading configurations from raw")
	}

	currentConfigs, err := m.configurations()
	if err != nil {
		return errors.Wrapf(err, "failed getting current configurations")
	}

	// - validate it making sure it contains a new TMS
	for _, config := range configurations {
		if err := config.Validate(); err != nil {
			return errors.Wrapf(err, "failed validating configuration [%s]", config.ID())
		}
		// If the TMS already exists, return an error
		for _, currentConfig := range currentConfigs {
			if currentConfig.ID().Equal(config.ID()) {
				return errors.Errorf("updating existing configuration is not supported [%s]", config.ID())
			}
		}
	}
	// If all good, merge into the main configuration service
	if err := m.cp.MergeConfig(raw); err != nil {
		return err
	}

	// append the new configurations
	// here we just need to reset the holder
	if err := m.configurationsHolder.Reset(); err != nil {
		return errors.Wrapf(err, "failed resetting configurations holder")
	}

	return nil
}

func (m *Service) configurations() (map[string]*Configuration, error) {
	return m.configurationsHolder.Get()
}

// ResetConfigurations resets the internal configurations holder.
func (m *Service) ResetConfigurations() error {
	return m.configurationsHolder.Reset()
}

type loader struct {
	cp         Provider
	validators []Validator
}

func (m *loader) load() (map[string]*Configuration, error) {
	// load
	var boxedConfig map[string]any
	if err := m.cp.UnmarshalKey(TMSPath, &boxedConfig); err != nil {
		logger.Debugf("cannot unmarshal configurations from [%s], try empty map: [%v]", TMSPath, err)
		boxedConfig = map[string]any{}
	}
	if boxedConfig == nil {
		logger.Debugf("configurations from [%s] is nil, return empty map", TMSPath)

		return map[string]*Configuration{}, nil
	}

	tmsConfigs := map[string]*Configuration{}
	for id := range boxedConfig {
		tmsID := driver.TMSID{}
		if err := m.cp.UnmarshalKey(config.Join(TMSPath, id), &tmsID); err != nil {
			return nil, errors.WithMessagef(err, "cannot load tms configuration for [%s]", id)
		}
		tmsConfigs[id] = NewConfiguration(m.cp, id, tmsID)
		if err := tmsConfigs[id].Validate(); err != nil {
			return nil, errors.WithMessagef(err, "cannot load configuration for [%s]", id)
		}
	}

	return tmsConfigs, nil
}

func (m *loader) close(map[string]*Configuration) error {
	return nil
}

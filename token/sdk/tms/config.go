/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tms

import (
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/config"
)

// ConfigServiceWrapper wraps the config service to provide driver.Configuration interface.
type ConfigServiceWrapper struct {
	*config.Service
}

// NewConfigServiceWrapper creates a new config service wrapper.
func NewConfigServiceWrapper(service *config.Service) *ConfigServiceWrapper {
	return &ConfigServiceWrapper{Service: service}
}

// Configurations returns all TMS configurations as driver.Configuration interfaces.
func (c *ConfigServiceWrapper) Configurations() ([]driver.Configuration, error) {
	configs, err := c.Service.Configurations()
	if err != nil {
		return nil, err
	}
	result := make([]driver.Configuration, len(configs))
	for i, config := range configs {
		result[i] = config
	}

	return result, nil
}

// ConfigurationFor returns the configuration for the specified network, channel, and namespace.
func (c *ConfigServiceWrapper) ConfigurationFor(network string, channel string, namespace string) (driver.Configuration, error) {
	return c.Service.ConfigurationFor(network, channel, namespace)
}
